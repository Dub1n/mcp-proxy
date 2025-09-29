package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/sync/errgroup"
)

// ===== infra helpers =====

type MiddlewareFunc func(http.Handler) http.Handler

func chainMiddleware(h http.Handler, middlewares ...MiddlewareFunc) http.Handler {
	for _, mw := range middlewares {
		h = mw(h)
	}
	return h
}

func newAuthMiddleware(tokens []string) MiddlewareFunc {
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		tokenSet[token] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// allow internal re-entry from the facade
			if r.Header.Get("X-Proxy-Internal") == "1" {
				next.ServeHTTP(w, r)
				return
			}
			if len(tokens) != 0 {
				token := r.Header.Get("Authorization")
				token = strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
				if token == "" {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				if _, ok := tokenSet[token]; !ok {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func loggerMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("<%s> %s %s", prefix, r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

func recoverMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Printf("<%s> panic: %v", prefix, err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// build a clean route like "/base/name/" with trailing slash
func routeFor(basePath, name string) string {
	route := path.Join(basePath, name)
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	if !strings.HasSuffix(route, "/") {
		route += "/"
	}
	return route
}

// capture-and-defer writer for internal mux re-entry
type responseRecorder struct {
	HeaderMap  http.Header
	Body       bytes.Buffer
	StatusCode int
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{
		HeaderMap:  make(http.Header),
		StatusCode: http.StatusOK,
	}
}

func (rr *responseRecorder) Header() http.Header         { return rr.HeaderMap }
func (rr *responseRecorder) WriteHeader(statusCode int)  { rr.StatusCode = statusCode }
func (rr *responseRecorder) Write(b []byte) (int, error) { return rr.Body.Write(b) }
func (rr *responseRecorder) FlushTo(w http.ResponseWriter) {
	for k, vv := range rr.HeaderMap {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(rr.StatusCode)
	_, _ = w.Write(rr.Body.Bytes())
}

type readinessSnapshot struct {
	ReadyAt     time.Time
	ServerCount int
}

var readyState atomic.Pointer[readinessSnapshot]

// ===== SSE facade =====

func emitReadinessEvent(w http.ResponseWriter, flusher http.Flusher) bool {
	snapshot := readyState.Load()
	if snapshot == nil {
		return false
	}
	payload := map[string]any{
		"state":       "ready",
		"readyAt":     snapshot.ReadyAt.Format(time.RFC3339Nano),
		"serverCount": snapshot.ServerCount,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("<facade> failed to marshal readiness payload: %v", err)
		return false
	}
	fmt.Fprintf(w, "event: ready\ndata: %s\n\n", data)
	flusher.Flush()
	return true
}

func handleSSE(w http.ResponseWriter, r *http.Request, endpoint string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// initial tick to open proxies
	_, _ = io.WriteString(w, ":\n\n")
	flusher.Flush()

	if endpoint != "" {
		if parsed, err := url.Parse(endpoint); err == nil {
			mountPath := parsed.Path
			if mountPath == "" {
				mountPath = "/mcp"
			}
			session := parsed.Query().Get("sessionId")
			if session == "" {
				session = parsed.Query().Get("session_id")
			}
			if session != "" {
				sessionHex := strings.ReplaceAll(session, "-", "")
				if mountPath == "" || mountPath[0] != '/' {
					mountPath = "/" + mountPath
				}
				endpointPath := fmt.Sprintf("%s?session_id=%s", mountPath, sessionHex)
				fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointPath)
				flusher.Flush()
				goto endpointDone
			}
		}
		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpoint)
		flusher.Flush()
	}

endpointDone:

	readyAnnounced := emitReadinessEvent(w, flusher)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	var (
		readyTicker *time.Ticker
		readyChan   <-chan time.Time
	)
	if !readyAnnounced {
		readyTicker = time.NewTicker(1 * time.Second)
		readyChan = readyTicker.C
	}

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			if readyTicker != nil {
				readyTicker.Stop()
			}
			return
		case <-ticker.C:
			_, _ = io.WriteString(w, ":\n\n")
			flusher.Flush()
			if !readyAnnounced {
				if emitReadinessEvent(w, flusher) {
					readyAnnounced = true
					if readyTicker != nil {
						readyTicker.Stop()
						readyTicker = nil
						readyChan = nil
					}
				}
			}
		case <-readyChan:
			if emitReadinessEvent(w, flusher) {
				readyAnnounced = true
				if readyTicker != nil {
					readyTicker.Stop()
					readyTicker = nil
				}
				readyChan = nil
			}
		}
	}
}

// ===== JSON-RPC helpers =====

type jsonrpcRequest struct {
	JSONRPC   string          `json:"jsonrpc"`
	ID        any             `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonrpcResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonrpcError `json:"error,omitempty"`
}

func rpcError(id any, code int, msg string) jsonrpcResponse {
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: msg},
	}
}

func rpcOK(id any, result any) jsonrpcResponse {
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

func buildManifestDocument(
	manifestCfg *ManifestConfig,
	baseURL *url.URL,
	r *http.Request,
	tools []mcp.Tool,
	prompts []mcp.Prompt,
	resources []mcp.Resource,
	templates []mcp.ResourceTemplate,
) map[string]any {
	if manifestCfg == nil {
		manifestCfg = &ManifestConfig{}
	}
	if baseURL == nil {
		baseURL = &url.URL{}
	}

	endpointPath := path.Join(baseURL.Path, "mcp")
	if !strings.HasPrefix(endpointPath, "/") {
		endpointPath = "/" + endpointPath
	}

	requestScheme := "https"
	if r != nil {
		if r.TLS == nil {
			requestScheme = "http"
			if baseURL.Scheme != "" {
				requestScheme = baseURL.Scheme
			}
		}
	} else if baseURL.Scheme != "" {
		requestScheme = baseURL.Scheme
	}

	requestHost := baseURL.Host
	if r != nil && r.Host != "" {
		requestHost = r.Host
	}

	endpointURL := (&url.URL{Scheme: requestScheme, Host: requestHost, Path: endpointPath}).String()

	resourceEntries := make([]any, 0, len(manifestCfg.Resources)+len(resources))
	for _, res := range manifestCfg.Resources {
		resourceEntries = append(resourceEntries, res)
	}
	for _, res := range resources {
		resourceEntries = append(resourceEntries, res)
	}

	toolDescriptors := make(map[string]map[string]any)
	for _, tool := range tools {
		descriptor := toolDescriptorFromServer(tool)
		if tool.Name == facadeSearchToolName {
			descriptor = mergeWithFacadeDefaults(descriptor, searchToolDescriptor())
		} else if tool.Name == facadeFetchToolName {
			descriptor = mergeWithFacadeDefaults(descriptor, fetchToolDescriptor())
		}
		if descriptor == nil {
			continue
		}
		if _, exists := toolDescriptors[tool.Name]; !exists {
			toolDescriptors[tool.Name] = descriptor
		}
	}

	if _, ok := toolDescriptors[facadeSearchToolName]; !ok {
		toolDescriptors[facadeSearchToolName] = searchManifestDescriptor()
	}
	if _, ok := toolDescriptors[facadeFetchToolName]; !ok {
		toolDescriptors[facadeFetchToolName] = fetchManifestDescriptor()
	}

	toolNames := make([]string, 0, len(toolDescriptors))
	for name := range toolDescriptors {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	toolEntries := make([]any, 0, len(toolNames))
	for _, name := range toolNames {
		toolEntries = append(toolEntries, toolDescriptors[name])
	}

	payload := map[string]any{
		"name":        manifestCfg.Name,
		"version":     manifestCfg.Version,
		"description": manifestCfg.Description,
		"tools":       toolEntries,
		"prompts":     prompts,
		"resources":   resourceEntries,
		"endpoint":    endpointPath,
		"endpointURL": endpointURL,
	}
	if len(templates) > 0 {
		payload["resourceTemplates"] = templates
	}
	return payload
}

func handleNotification(w http.ResponseWriter, req *jsonrpcRequest) bool {
	if req == nil || req.ID != nil {
		return false
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// ===== main HTTP server =====

func startHTTPServer(config *Config) error {
	baseURL, uErr := url.Parse(config.McpProxy.BaseURL)
	if uErr != nil {
		return uErr
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var eg errgroup.Group
	httpMux := http.NewServeMux()

	// all connected servers
	servers := make(map[string]*Server)

	// catalog indexes (name/uri -> serverName) + readiness state
	var (
		indexMu       sync.RWMutex
		toolIndex     = make(map[string]string)
		promptIndex   = make(map[string]string)
		resourceIndex = make(map[string]string)
		clientsReady  atomic.Bool
	)

	// helper to rebuild index from current servers
	rebuildIndex := func() {
		tmpTools := make(map[string]string)
		tmpPrompts := make(map[string]string)
		tmpResources := make(map[string]string)
		for name, srv := range servers {
			for _, t := range srv.tools {
				tmpTools[t.Name] = name
			}
			for _, p := range srv.prompts {
				tmpPrompts[p.Name] = name
			}
			for _, res := range srv.resources {
				tmpResources[res.URI] = name
			}
		}
		indexMu.Lock()
		toolIndex = tmpTools
		promptIndex = tmpPrompts
		resourceIndex = tmpResources
		indexMu.Unlock()
	}

	// ---- manifest handler (single public endpoint) ----
	manifestCfg := config.Manifest
	if manifestCfg == nil {
		manifestCfg = &ManifestConfig{
			Name:        config.McpProxy.Name,
			Version:     config.McpProxy.Version,
			Description: "",
		}
	}

	httpMux.HandleFunc("/.well-known/mcp/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		allTools := make([]mcp.Tool, 0)
		allPrompts := make([]mcp.Prompt, 0)
		allResources := make([]mcp.Resource, 0)
		allResourceTemplates := make([]mcp.ResourceTemplate, 0)

		for _, srv := range servers {
			allTools = append(allTools, srv.tools...)
			allPrompts = append(allPrompts, srv.prompts...)
			allResources = append(allResources, srv.resources...)
			allResourceTemplates = append(allResourceTemplates, srv.resourceTemplates...)
		}

		doc := buildManifestDocument(manifestCfg, baseURL, r, allTools, allPrompts, allResources, allResourceTemplates)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})

	// ---- build servers and mount per-server handlers ----
	info := mcp.Implementation{Name: config.McpProxy.Name}

	for name, clientConfig := range config.McpServers {
		mcpClient, err := newMCPClient(name, clientConfig)
		if err != nil {
			return err
		}
		server, err := newMCPServer(name, config.McpProxy, clientConfig)
		if err != nil {
			return err
		}
		servers[name] = server

		nameCopy := name
		clientConfigCopy := clientConfig
		mcpClientCopy := mcpClient
		serverCopy := server

		eg.Go(func() error {
			log.Printf("<%s> Connecting", nameCopy)
			if addErr := mcpClientCopy.addToMCPServer(ctx, info, serverCopy); addErr != nil {
				log.Printf("<%s> Failed to add client to server: %v", nameCopy, addErr)
				if clientConfigCopy.Options.PanicIfInvalid.OrElse(false) {
					return addErr
				}
				return nil
			}
			log.Printf("<%s> Connected", nameCopy)

			// add route for this server
			mws := []MiddlewareFunc{recoverMiddleware(nameCopy)}
			if clientConfigCopy.Options.LogEnabled.OrElse(false) {
				mws = append(mws, loggerMiddleware(nameCopy))
			}
			if len(clientConfigCopy.Options.AuthTokens) > 0 {
				mws = append(mws, newAuthMiddleware(clientConfigCopy.Options.AuthTokens))
			}
			mcpRoute := routeFor(baseURL.Path, nameCopy)
			log.Printf("<%s> Handling requests at %s", nameCopy, mcpRoute)
			httpMux.Handle(mcpRoute, chainMiddleware(serverCopy.handler, mws...))

			// index catalog entries for this server
			indexMu.Lock()
			for _, t := range serverCopy.tools {
				toolIndex[t.Name] = nameCopy
			}
			for _, p := range serverCopy.prompts {
				promptIndex[p.Name] = nameCopy
			}
			for _, res := range serverCopy.resources {
				resourceIndex[res.URI] = nameCopy
			}
			indexMu.Unlock()

			return nil
		})
	}

	// mark ready once all client goroutines return (success or tolerated failure)
	go func() {
		if err := eg.Wait(); err != nil {
			log.Fatalf("Failed to initialize clients: %v", err)
		}
		clientsReady.Store(true)
		log.Printf("All clients initialized")
		snapshot := &readinessSnapshot{
			ReadyAt:     time.Now().UTC(),
			ServerCount: len(config.McpServers),
		}
		readyState.Store(snapshot)
		log.Printf("<facade> Ready: downstream servers=%d readyAt=%s", snapshot.ServerCount, snapshot.ReadyAt.Format(time.RFC3339Nano))
	}()

	// helper: try multiple internal POST targets for a server and return the first 2xx
	tryDispatch := func(serverName string, body []byte, r *http.Request, rr *responseRecorder) (chosen string, status int) {
		base := routeFor(baseURL.Path, serverName)
		paths := []string{
			path.Join(base, "mcp"),
			base,                          // "/<name>/"
			strings.TrimSuffix(base, "/"), // "/<name>"
			path.Join(base, "message"),
			path.Join(base, "messages"),
			path.Join(base, "send"),
			path.Join(base, "rpc"),
			path.Join(base, "jsonrpc"),
		}
		for _, p := range paths {
			r2 := r.Clone(r.Context())
			r2.Method = http.MethodPost
			r2.URL = &url.URL{Path: p}
			r2.RequestURI = ""
			r2.Body = io.NopCloser(bytes.NewReader(body))
			r2.Header = r.Header.Clone()
			r2.Header.Set("X-Proxy-Internal", "1") // <— add this line
			if r2.Header.Get("Content-Type") == "" {
				r2.Header.Set("Content-Type", "application/json")
			}

			tmp := newResponseRecorder()
			httpMux.ServeHTTP(tmp, r2)
			if tmp.StatusCode >= 200 && tmp.StatusCode <= 204 {
				*rr = *tmp
				return p, tmp.StatusCode
			}
		}
		// none matched; surface the best info from the last attempt
		last := paths[len(paths)-1]
		return last, http.StatusNotFound
	}

	// ---- /mcp facade ----
	mcpPath := path.Join(baseURL.Path, "mcp")
	if !strings.HasPrefix(mcpPath, "/") {
		mcpPath = "/" + mcpPath
	}
	httpMux.HandleFunc(mcpPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("<facade> %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")
			w.Header().Set("mcp-session-id", uuid.New().String())
			w.WriteHeader(http.StatusOK)
			log.Printf("<facade> %s %s?%s -> %d", r.Method, r.URL.Path, r.URL.RawQuery, http.StatusOK)
			return

		case http.MethodGet:
			publicEndpoint := baseURL.ResolveReference(&url.URL{Path: path.Join(baseURL.Path, "mcp")})
			sessionID := uuid.New().String()
			messageEndpoint := fmt.Sprintf("%s?sessionId=%s", publicEndpoint.String(), sessionID)
			w.Header().Set("mcp-session-id", sessionID)
			log.Printf("<facade> SSE session=%s endpoint=%s", sessionID, messageEndpoint)
			handleSSE(w, r, messageEndpoint)
			log.Printf("<facade> %s %s?%s -> %d", r.Method, r.URL.Path, r.URL.RawQuery, http.StatusOK)
			return

		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if len(body) == 0 {
				body = []byte(`{}`)
			}

			// if batch, politely decline (facade can add later)
			if len(body) > 0 && (body[0] == '[') {
				var batch []jsonrpcRequest
				if err := json.Unmarshal(body, &batch); err != nil {
					http.Error(w, "Bad Request", http.StatusBadRequest)
					log.Printf("<facade> %s %s?%s invalid batch: %v", r.Method, r.URL.Path, r.URL.RawQuery, err)
					return
				}
				out := make([]jsonrpcResponse, 0, len(batch))
				for _, req := range batch {
					out = append(out, rpcError(req.ID, -32601, "Batch not supported by facade"))
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
				log.Printf("<facade> %s %s?%s batch -> %d", r.Method, r.URL.Path, r.URL.RawQuery, http.StatusOK)
				return
			}

			var req jsonrpcRequest
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "Bad Request", http.StatusBadRequest)
				log.Printf("<facade> %s %s?%s invalid json: %v", r.Method, r.URL.Path, r.URL.RawQuery, err)
				return
			}

			if handleNotification(w, &req) {
				log.Printf("<facade> notification %s", req.Method)
				return
			}

			switch req.Method {
			case "initialize":
				// wait briefly for readiness (up to 2s) so we can return a non-empty catalog
				deadline := time.Now().Add(2 * time.Second)
				waited := false
				for !clientsReady.Load() && time.Now().Before(deadline) {
					waited = true
					time.Sleep(50 * time.Millisecond)
				}
				if waited {
					w.Header().Set("X-Proxy-Waited-For-Init", "true")
				}

				result := buildInitializeResult(config, servers)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcOK(req.ID, result))
				return

			case "tools/list":
				// same readiness wait
				deadline := time.Now().Add(2 * time.Second)
				waited := false
				for !clientsReady.Load() && time.Now().Before(deadline) {
					waited = true
					time.Sleep(50 * time.Millisecond)
				}
				if waited {
					w.Header().Set("X-Proxy-Waited-For-Init", "true")
				}

				items := collectTools(servers)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcOK(req.ID, map[string]any{"tools": items}))
				return

			case "prompts/list":
				deadline := time.Now().Add(2 * time.Second)
				waited := false
				for !clientsReady.Load() && time.Now().Before(deadline) {
					waited = true
					time.Sleep(50 * time.Millisecond)
				}
				if waited {
					w.Header().Set("X-Proxy-Waited-For-Init", "true")
				}
				items := collectPrompts(servers)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcOK(req.ID, map[string]any{"prompts": items}))
				return

			case "prompts/get":
				var p struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments,omitempty"`
				}
				if len(req.Params) > 0 {
					_ = json.Unmarshal(req.Params, &p)
				}
				if p.Name == "" {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32602, "Missing prompt name"))
					return
				}
				indexMu.RLock()
				serverName, ok := promptIndex[p.Name]
				indexMu.RUnlock()
				if !ok {
					rebuildIndex()
					indexMu.RLock()
					serverName, ok = promptIndex[p.Name]
					indexMu.RUnlock()
				}
				if !ok {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32601, "Unknown prompt: "+p.Name))
					log.Printf("<facade> prompts/get unknown prompt=%s", p.Name)
					return
				}
				rr := newResponseRecorder()
				chosen, status := tryDispatch(serverName, body, r, rr)
				w.Header().Set("X-Proxy-Dispatched-Server", serverName)
				w.Header().Set("X-Proxy-Internal-Path", chosen)
				w.Header().Set("X-Proxy-Internal-Status", http.StatusText(status))
				if status >= 200 && status <= 204 {
					rr.FlushTo(w)
					log.Printf("<facade> prompts/get prompt=%s server=%s path=%s status=%d", p.Name, serverName, chosen, status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32004, "Upstream rejected all candidate endpoints for server "+serverName))
				log.Printf("<facade> prompts/get failed prompt=%s server=%s path=%s status=%d", p.Name, serverName, chosen, status)
				return

			case "resources/list":
				deadline := time.Now().Add(2 * time.Second)
				waited := false
				for !clientsReady.Load() && time.Now().Before(deadline) {
					waited = true
					time.Sleep(50 * time.Millisecond)
				}
				if waited {
					w.Header().Set("X-Proxy-Waited-For-Init", "true")
				}
				items := collectResources(servers)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcOK(req.ID, map[string]any{"resources": items}))
				return

			case "resources/read":
				var p struct {
					URI string `json:"uri"`
				}
				if len(req.Params) > 0 {
					_ = json.Unmarshal(req.Params, &p)
				}
				if p.URI == "" {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32602, "Missing resource uri"))
					return
				}
				indexMu.RLock()
				serverName, ok := resourceIndex[p.URI]
				indexMu.RUnlock()
				if !ok {
					rebuildIndex()
					indexMu.RLock()
					serverName, ok = resourceIndex[p.URI]
					indexMu.RUnlock()
				}
				if !ok {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32601, "Unknown resource: "+p.URI))
					log.Printf("<facade> resources/read unknown uri=%s", p.URI)
					return
				}
				rr := newResponseRecorder()
				chosen, status := tryDispatch(serverName, body, r, rr)
				w.Header().Set("X-Proxy-Dispatched-Server", serverName)
				w.Header().Set("X-Proxy-Internal-Path", chosen)
				w.Header().Set("X-Proxy-Internal-Status", http.StatusText(status))
				if status >= 200 && status <= 204 {
					rr.FlushTo(w)
					log.Printf("<facade> resources/read uri=%s server=%s path=%s status=%d", p.URI, serverName, chosen, status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32004, "Upstream rejected all candidate endpoints for server "+serverName))
				log.Printf("<facade> resources/read failed uri=%s server=%s path=%s status=%d", p.URI, serverName, chosen, status)
				return

			case "resources/templates/list":
				deadline := time.Now().Add(2 * time.Second)
				waited := false
				for !clientsReady.Load() && time.Now().Before(deadline) {
					waited = true
					time.Sleep(50 * time.Millisecond)
				}
				if waited {
					w.Header().Set("X-Proxy-Waited-For-Init", "true")
				}
				items := collectResourceTemplates(servers)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcOK(req.ID, map[string]any{"resourceTemplates": items}))
				return

			case "ping":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcOK(req.ID, map[string]any{}))
				return

			case facadeSearchToolName:
				var p struct {
					Query string `json:"query"`
				}
				if len(req.Params) > 0 {
					_ = json.Unmarshal(req.Params, &p)
				}
				w.Header().Set("Content-Type", "application/json")
				payload := buildFacadeSearchPayload(p.Query)
				_ = json.NewEncoder(w).Encode(rpcOK(req.ID, payload))
				if results, ok := payload["results"].([]map[string]any); ok {
					log.Printf("<facade> search (static) query=%q hits=%d", p.Query, len(results))
				} else {
					log.Printf("<facade> search (static) query=%q", p.Query)
				}
				return

			case "tools/call":
				// ensure we have an index; rebuild lazily if empty
				indexMu.RLock()
				idxEmpty := len(toolIndex) == 0
				indexMu.RUnlock()
				if idxEmpty {
					rebuildIndex()
					w.Header().Set("X-Proxy-Rebuilt-Index", "true")
				}

				var p struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
					Stream    bool            `json:"stream,omitempty"`
				}
				if len(req.Params) > 0 {
					_ = json.Unmarshal(req.Params, &p)
				}
				if p.Name == "" {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32602, "Missing tool name"))
					return
				}

				if p.Name == facadeSearchToolName {
					var searchArgs struct {
						Query string `json:"query"`
					}
					if len(p.Arguments) > 0 {
						_ = json.Unmarshal(p.Arguments, &searchArgs)
					}
					w.Header().Set("Content-Type", "application/json")
					payload := buildFacadeSearchPayload(searchArgs.Query)
					_ = json.NewEncoder(w).Encode(rpcOK(req.ID, payload))
					if results, ok := payload["results"].([]map[string]any); ok {
						log.Printf("<facade> tools/call search (static) query=%q hits=%d", searchArgs.Query, len(results))
					} else {
						log.Printf("<facade> tools/call search (static) query=%q", searchArgs.Query)
					}
					return
				}

				if p.Name == facadeFetchToolName {
					var fetchArgs struct {
						ID string `json:"id"`
					}
					if len(p.Arguments) > 0 {
						_ = json.Unmarshal(p.Arguments, &fetchArgs)
					}
					if fetchArgs.ID == "" {
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32602, "Missing fetch id"))
						return
					}
					if payload, ok := buildFacadeFetchPayload(fetchArgs.ID); ok {
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(rpcOK(req.ID, payload))
						log.Printf("<facade> tools/call fetch (static) id=%q", fetchArgs.ID)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32005, "Unknown fetch id"))
					log.Printf("<facade> tools/call fetch unknown id=%s", fetchArgs.ID)
					return
				}

				indexMu.RLock()
				serverName, ok := toolIndex[p.Name]
				indexMu.RUnlock()
				if !ok {
					// last-ditch: rebuild and check again
					rebuildIndex()
					indexMu.RLock()
					serverName, ok = toolIndex[p.Name]
					indexMu.RUnlock()
				}
				if !ok {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32601, "Unknown tool: "+p.Name))
					log.Printf("<facade> tools/call unknown tool=%s", p.Name)
					return
				}

				// forward to the server using adaptive path candidates
				rr := newResponseRecorder()
				chosen, status := tryDispatch(serverName, body, r, rr)

				w.Header().Set("X-Proxy-Dispatched-Server", serverName)
				w.Header().Set("X-Proxy-Internal-Path", chosen)
				w.Header().Set("X-Proxy-Internal-Status", http.StatusText(status))

				if status >= 200 && status <= 204 {
					rr.FlushTo(w)
					log.Printf("<facade> tools/call tool=%s server=%s path=%s status=%d", p.Name, serverName, chosen, status)
					return
				}

				// none succeeded: protocol-level error rather than transport 404
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32004, "Upstream rejected all candidate endpoints for server "+serverName))
				log.Printf("<facade> tools/call failed tool=%s server=%s path=%s status=%d", p.Name, serverName, chosen, status)
				return

			default:
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rpcError(req.ID, -32601, "Method not found"))
				log.Printf("<facade> unsupported method=%s", req.Method)
				return
			}

		case http.MethodOptions:
			w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return

		default:
			w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			log.Printf("<facade> %s %s?%s -> %d", r.Method, r.URL.Path, r.URL.RawQuery, http.StatusMethodNotAllowed)
			return
		}
	})

	// ---- start & shutdown ----
	httpServer := &http.Server{
		Addr:    config.McpProxy.Addr,
		Handler: httpMux,
	}

	go func() {
		log.Printf("Starting %s server", config.McpProxy.Type)
		log.Printf("%s server listening on %s", config.McpProxy.Type, config.McpProxy.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Shutdown signal received")

	shutdownCtx, cancelShutdown := context.WithTimeout(ctx, 5*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
