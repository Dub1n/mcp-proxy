package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestBuildInitializeResultIncludesServerInfo(t *testing.T) {
	cfg := &Config{
		McpProxy: &MCPProxyConfigV2{
			Name:    "Proxy",
			Version: "1.2.3",
		},
	}

	servers := map[string]*Server{
		"alpha": {
			tools: []mcp.Tool{
				{
					Name:        "echo",
					Description: "Echo back input",
					InputSchema: mcp.ToolInputSchema{Type: "object"},
				},
			},
			prompts: []mcp.Prompt{
				{
					Name:        "greet",
					Description: "Say hi",
					Arguments:   []mcp.PromptArgument{{Name: "name", Required: true}},
				},
			},
			resources: []mcp.Resource{
				{
					URI:         "resource://alpha/info",
					Name:        "info",
					Description: "Alpha info",
					MIMEType:    "text/plain",
				},
			},
			resourceTemplates: []mcp.ResourceTemplate{
				{
					Name:        "docs",
					Description: "Docs template",
				},
			},
		},
	}

	result := buildInitializeResult(cfg, servers, nil)

	serverInfoValue, ok := result["serverInfo"]
	if !ok {
		t.Fatalf("expected serverInfo in result")
	}

	serverInfo, ok := serverInfoValue.(map[string]any)
	if !ok {
		t.Fatalf("expected serverInfo to be map but got %T", serverInfoValue)
	}

	if serverInfo["name"] != cfg.McpProxy.Name {
		t.Fatalf("serverInfo.name = %v, want %s", serverInfo["name"], cfg.McpProxy.Name)
	}

	if serverInfo["version"] != cfg.McpProxy.Version {
		t.Fatalf("serverInfo.version = %v, want %s", serverInfo["version"], cfg.McpProxy.Version)
	}

	capabilitiesValue, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities map but got %T", result["capabilities"])
	}

	if _, ok := capabilitiesValue["tools"]; !ok {
		t.Fatalf("expected tools capability to be advertised")
	}
	if _, ok := capabilitiesValue["prompts"]; !ok {
		t.Fatalf("expected prompts capability to be advertised")
	}
	if _, ok := capabilitiesValue["resources"]; !ok {
		t.Fatalf("expected resources capability to be advertised")
	}

	toolsValue, ok := result["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("expected tools list but got %T", result["tools"])
	}

	if len(toolsValue) != 3 {
		t.Fatalf("expected tools to include echo, search, fetch; got %d", len(toolsValue))
	}

	found := make(map[string]map[string]any)
	for _, tool := range toolsValue {
		name, _ := tool["name"].(string)
		found[name] = tool
	}

	if _, ok := found[facadeSearchToolName]; !ok {
		t.Fatalf("expected facade search tool present")
	}
	if _, ok := found[facadeFetchToolName]; !ok {
		t.Fatalf("expected facade fetch tool present")
	}
	if _, ok := found["echo"]; !ok {
		t.Fatalf("expected echo tool present")
	}

	promptsValue, ok := result["prompts"].([]map[string]any)
	if !ok {
		t.Fatalf("expected prompts list but got %T", result["prompts"])
	}
	if len(promptsValue) != 1 || promptsValue[0]["name"] != "greet" {
		t.Fatalf("expected prompt greet present")
	}

	resourcesValue, ok := result["resources"].([]map[string]any)
	if !ok {
		t.Fatalf("expected resources list but got %T", result["resources"])
	}
	if len(resourcesValue) != 1 || resourcesValue[0]["uri"] != "resource://alpha/info" {
		t.Fatalf("expected resource uri resource://alpha/info present")
	}
}

func TestHandleNotificationWithInitialized(t *testing.T) {
	req := &jsonrpcRequest{Method: "notifications/initialized"}
	w := httptest.NewRecorder()
	if handled := handleNotification(w, req); !handled {
		t.Fatalf("expected notification to be handled")
	}
	if w.Result().StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 status, got %d", w.Result().StatusCode)
	}
}

func TestHandleNotificationSkipsRequestsWithID(t *testing.T) {
	req := &jsonrpcRequest{Method: "initialize", ID: 1}
	w := httptest.NewRecorder()
	if handled := handleNotification(w, req); handled {
		t.Fatalf("expected non-notification request to be ignored")
	}
}

func TestBuildManifestDocumentIncludesFacadeAndServerTools(t *testing.T) {
	manifestCfg := &ManifestConfig{Name: "Proxy", Version: "1.0.0", Description: ""}
	baseURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatalf("failed to parse base URL: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/.well-known/mcp/manifest.json", nil)

	allTools := []mcp.Tool{
		{Name: facadeFetchToolName},
		{Name: facadeSearchToolName},
		{Name: "extra"},
	}

	doc := buildManifestDocument(manifestCfg, baseURL, req, allTools, nil, nil, nil)

	rawTools, ok := doc["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools slice, got %T", doc["tools"])
	}
	if len(rawTools) != 3 {
		t.Fatalf("expected fetch, search, and extra tools, got %d", len(rawTools))
	}

	found := map[string]bool{}
	for _, entry := range rawTools {
		switch v := entry.(type) {
		case mcp.Tool:
			found[v.Name] = true
		case map[string]any:
			if name, _ := v["name"].(string); name != "" {
				found[name] = true
			}
		default:
			t.Fatalf("unexpected tool descriptor type %T", entry)
		}
	}

	if !found[facadeFetchToolName] {
		t.Fatalf("fetch tool missing from manifest")
	}
	if !found[facadeSearchToolName] {
		t.Fatalf("search tool missing from manifest")
	}
	if !found["extra"] {
		t.Fatalf("expected extra tool from upstream to be present")
	}
}

func TestManifestServerEntriesIncludesOnlyAggregator(t *testing.T) {
	cfg := &Config{McpProxy: &MCPProxyConfigV2{Name: "Proxy", Type: MCPServerTypeStreamable, Version: "1.0.0"}}
	manifestCfg := &ManifestConfig{ServerName: "stelae"}
	doc := map[string]any{"endpointURL": "https://example.com/mcp"}

	entries := manifestServerEntries(cfg, manifestCfg, doc)
	if len(entries) != 1 {
		t.Fatalf("expected single server entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry["name"] != "stelae" {
		t.Fatalf("expected server name stelae, got %v", entry["name"])
	}
	if entry["transport"] != "streamable-http" {
		t.Fatalf("expected transport streamable-http, got %v", entry["transport"])
	}
	if entry["url"] != "https://example.com/mcp" {
		t.Fatalf("unexpected endpoint url: %v", entry["url"])
	}
	if entry["version"] != "1.0.0" {
		t.Fatalf("unexpected version: %v", entry["version"])
	}
}

func TestToolOverridesApplyAnnotations(t *testing.T) {
	trueVal := true
	alias := "fs_read_file"
	description := "Read file via proxy"
	title := "Read File (Proxy)"
	inputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}
	outputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": map[string]any{"type": "string"},
		},
		"required": []any{"result"},
	}
	manifestCfg := &ManifestConfig{
		Name: "Proxy",
		ToolOverrides: map[string]*ToolOverrideConfig{
			"read_file": {
				Name:         &alias,
				Description:  &description,
				Annotations:  &AnnotationOverrideConfig{ReadOnlyHint: &trueVal, Title: &title},
				InputSchema:  inputSchema,
				OutputSchema: outputSchema,
			},
		},
	}
	config := &Config{
		McpProxy: &MCPProxyConfigV2{Name: "Proxy"},
		Manifest: manifestCfg,
	}
	servers := map[string]*Server{
		"fs": {
			tools: []mcp.Tool{{Name: "read_file"}},
		},
	}

	set := &ToolOverrideSet{
		ToolOverrides: copyToolOverrideMap(manifestCfg.ToolOverrides),
		Servers:       make(map[string]*toolOverrideFragment),
		Aliases:       make(map[string]string),
		Renamed:       make(map[string]string),
	}
	sanitizeToolOverrideSet(set)
	tools := collectTools(servers, set)
	if len(tools) == 0 {
		t.Fatalf("expected tools from collectTools")
	}
	found := false
	for _, tool := range tools {
		if tool["name"] != "fs_read_file" {
			continue
		}
		annotations, _ := tool["annotations"].(map[string]any)
		if annotations == nil {
			t.Fatalf("expected annotations map for read_file")
		}
		if v, ok := annotations["readOnlyHint"].(bool); !ok || !v {
			t.Fatalf("expected readOnlyHint override true, got %v", annotations["readOnlyHint"])
		}
		if titleVal, ok := annotations["title"].(string); !ok || titleVal != "Read File (Proxy)" {
			t.Fatalf("expected title override, got %v", annotations["title"])
		}
		if desc, ok := tool["description"].(string); !ok || desc != "Read file via proxy" {
			t.Fatalf("expected description override, got %v", tool["description"])
		}
		if name, ok := tool["name"].(string); !ok || name != "fs_read_file" {
			t.Fatalf("expected alias applied, got %v", tool["name"])
		}
		if schema, ok := tool["inputSchema"].(map[string]any); !ok || schema["type"] != "object" {
			t.Fatalf("expected inputSchema override, got %v", tool["inputSchema"])
		}
		if schema, ok := tool["outputSchema"].(map[string]any); !ok || schema["type"] != "object" {
			t.Fatalf("expected outputSchema override, got %v", tool["outputSchema"])
		}
		meta, _ := tool["x-stelae"].(map[string]any)
		if meta == nil {
			t.Fatalf("expected x-stelae metadata for tool")
		}
		var servers []string
		if raw, ok := meta["servers"].([]string); ok {
			servers = raw
		} else if rawAny, ok := meta["servers"].([]any); ok {
			for _, entry := range rawAny {
				if name, ok := entry.(string); ok {
					servers = append(servers, name)
				}
			}
		}
		if len(servers) != 1 {
			t.Fatalf("expected single x-stelae server entry, got %v", meta["servers"])
		}
		if servers[0] != "fs" {
			t.Fatalf("expected x-stelae servers to include fs, got %v", meta["servers"])
		}
		if primary, _ := meta["primaryServer"].(string); primary != "fs" {
			t.Fatalf("expected primaryServer fs, got %v", meta["primaryServer"])
		}
		found = true
	}
	if !found {
		t.Fatalf("read_file tool not found in collectTools output")
	}

	baseURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatalf("failed to parse base URL: %v", err)
	}
	doc := buildManifestDocumentWithOverrides(manifestCfg, baseURL, nil, []mcp.Tool{{Name: "read_file"}}, nil, nil, nil, set)
	rawTools, ok := doc["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array in manifest")
	}
	found = false
	for _, entry := range rawTools {
		manifestTool := entry.(map[string]any)
		if manifestTool["name"] != "fs_read_file" {
			continue
		}
		manifestAnnotations, _ := manifestTool["annotations"].(map[string]any)
		if v, ok := manifestAnnotations["readOnlyHint"].(bool); !ok || !v {
			t.Fatalf("expected manifest readOnlyHint override true, got %v", manifestAnnotations["readOnlyHint"])
		}
		if titleVal, ok := manifestAnnotations["title"].(string); !ok || titleVal != "Read File (Proxy)" {
			t.Fatalf("expected manifest title override, got %v", manifestAnnotations["title"])
		}
		if desc, ok := manifestTool["description"].(string); !ok || desc != "Read file via proxy" {
			t.Fatalf("expected manifest description override, got %v", manifestTool["description"])
		}
		if name, ok := manifestTool["name"].(string); !ok || name != "fs_read_file" {
			t.Fatalf("expected manifest alias applied, got %v", manifestTool["name"])
		}
		if schema, ok := manifestTool["inputSchema"].(map[string]any); !ok || schema["type"] != "object" {
			t.Fatalf("expected manifest inputSchema override, got %v", manifestTool["inputSchema"])
		}
		if schema, ok := manifestTool["outputSchema"].(map[string]any); !ok || schema["type"] != "object" {
			t.Fatalf("expected manifest outputSchema override, got %v", manifestTool["outputSchema"])
		}
		found = true
	}
	if !found {
		t.Fatalf("read_file tool not found in manifest output")
	}

	init := buildInitializeResult(config, servers, set)
	initTools, ok := init["tools"].([]map[string]any)
	if !ok || len(initTools) == 0 {
		t.Fatalf("expected tools in initialize result")
	}
	found = false
	for _, tool := range initTools {
		if tool["name"] != "fs_read_file" {
			continue
		}
		ann, _ := tool["annotations"].(map[string]any)
		if v, ok := ann["readOnlyHint"].(bool); !ok || !v {
			t.Fatalf("expected initialize readOnlyHint override true, got %v", ann)
		}
		if annTitle, ok := ann["title"].(string); !ok || annTitle != "Read File (Proxy)" {
			t.Fatalf("expected initialize title override, got %v", ann["title"])
		}
		if desc, ok := tool["description"].(string); !ok || desc != "Read file via proxy" {
			t.Fatalf("expected initialize description override, got %v", tool["description"])
		}
		if name, ok := tool["name"].(string); !ok || name != "fs_read_file" {
			t.Fatalf("expected initialize alias applied, got %v", tool["name"])
		}
		found = true
	}
	if !found {
		t.Fatalf("read_file tool not found in initialize output")
	}
}

func TestToolsListHTTPHandlerReturnsCatalog(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	servers := map[string]*Server{
		"alpha": {
			transport: MCPServerTypeStreamable,
			tools:     []mcp.Tool{{Name: "fetch"}},
		},
	}
	handler := toolsListHTTPHandler(&ready, servers, nil)
	req := httptest.NewRequest(http.MethodGet, "/tools/list", nil)
	resp := httptest.NewRecorder()
	handler(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode tools payload: %v", err)
	}
	items, ok := payload["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array in payload")
	}
	foundFetch := false
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if name, _ := m["name"].(string); name == "fetch" {
				foundFetch = true
			}
		}
	}
	if !foundFetch {
		t.Fatalf("expected fetch tool in payload: %+v", items)
	}
}

func TestToolsListHTTPHandlerRejectsNonGET(t *testing.T) {
	handler := toolsListHTTPHandler(&atomic.Bool{}, map[string]*Server{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/tools/list", nil)
	resp := httptest.NewRecorder()
	handler(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.Code)
	}
	if allow := resp.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("expected Allow header 'GET', got %q", allow)
	}
}

func TestStreamAliasHandlerForwardsToMCP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := streamAliasHandler(mux, "/mcp")
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	resp := httptest.NewRecorder()
	handler(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected forwarded status 204, got %d", resp.Code)
	}
}

func TestCollectToolsIncludesFacadeAndServerCatalog(t *testing.T) {
	servers := map[string]*Server{
		"alpha": {
			tools: []mcp.Tool{
				{
					Name:        facadeSearchToolName,
					Description: "Workspace search",
					InputSchema: mcp.ToolInputSchema{Type: "object", Required: []string{"query"}},
				},
				{
					Name:        "summarize",
					Description: "Summarize documents",
					InputSchema: mcp.ToolInputSchema{Type: "object"},
				},
			},
		},
		"beta": {
			tools: []mcp.Tool{
				{
					Name:        facadeFetchToolName,
					Description: "Document fetch",
					InputSchema: mcp.ToolInputSchema{Type: "object", Required: []string{"url"}},
				},
			},
		},
	}

	tools := collectTools(servers, nil)
	if len(tools) != 3 {
		t.Fatalf("expected facade search/fetch plus summarize, got %d", len(tools))
	}

	fetched := make(map[string]map[string]any)
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		fetched[name] = tool
	}

	search, ok := fetched[facadeSearchToolName]
	if !ok {
		t.Fatalf("expected search tool present after filtering")
	}
	if desc := search["description"]; desc != "Workspace search" {
		t.Fatalf("search descriptor description = %v, want %q", desc, "Workspace search")
	}
	assertSchemaContains(t, search["inputSchema"], "query")

	fetch, ok := fetched[facadeFetchToolName]
	if !ok {
		t.Fatalf("expected fetch tool present after filtering")
	}
	assertSchemaContains(t, fetch["inputSchema"], "id")

	if summarize, extraPresent := fetched["summarize"]; !extraPresent {
		t.Fatalf("expected summarize tool to be present")
	} else if desc := summarize["description"]; desc != "Summarize documents" {
		t.Fatalf("summarize descriptor description = %v, want %q", desc, "Summarize documents")
	}
}

func TestCollectToolsProvidesFacadeFallbacks(t *testing.T) {
	tools := collectTools(map[string]*Server{}, nil)
	if len(tools) != 2 {
		t.Fatalf("expected facade fallback tools, got %d entries", len(tools))
	}
	fetched := make(map[string]bool)
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		fetched[name] = true
		requiredField := map[string]string{
			facadeSearchToolName: "query",
			facadeFetchToolName:  "id",
		}[name]
		if requiredField != "" {
			assertSchemaContains(t, tool["inputSchema"], requiredField)
		}
	}
	if !fetched[facadeSearchToolName] {
		t.Fatalf("expected fallback search tool present")
	}
	if !fetched[facadeFetchToolName] {
		t.Fatalf("expected fallback fetch tool present")
	}
}

func assertSchemaContains(t *testing.T, schemaValue any, requiredField string) {
	t.Helper()
	if requiredField == "" {
		return
	}
	data, err := json.Marshal(schemaValue)
	if err != nil {
		t.Fatalf("failed to marshal schema for %q: %v", requiredField, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to decode schema for %q: %v", requiredField, err)
	}
	requiredRaw, ok := decoded["required"].([]any)
	if !ok || len(requiredRaw) == 0 {
		t.Fatalf("schema missing required list for %q: %v", requiredField, decoded)
	}
	for _, item := range requiredRaw {
		if str, ok := item.(string); ok && str == requiredField {
			return
		}
	}
	t.Fatalf("schema missing required field %q: %v", requiredField, decoded)
}

func TestBuildFacadeSearchPayloadReturnsDeterministicHits(t *testing.T) {
	payload := buildFacadeSearchPayload("connector compliance")
	resultsValue, ok := payload["results"]
	if !ok {
		t.Fatalf("expected results key in payload")
	}
	results, ok := resultsValue.([]map[string]any)
	if !ok {
		t.Fatalf("expected results slice but got %T", resultsValue)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one search hit")
	}

	expectedIDs := map[string]bool{
		"repo:docs/SPEC-v1.md":                               true,
		"repo:dev/chat_gpt_connector_compliant_reference.md": true,
		"repo:dev/compliance_handoff.md":                     true,
	}

	for _, entry := range results {
		id, _ := entry["id"].(string)
		if !expectedIDs[id] {
			t.Fatalf("unexpected search id %q", id)
		}
		if _, ok := entry["text"].(string); !ok {
			t.Fatalf("expected search hit %q to include text", id)
		}
		metadata, ok := entry["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("expected metadata map for hit %q", id)
		}
		if snippet, _ := metadata["snippet"].(string); snippet == "" {
			t.Fatalf("expected snippet for hit %q", id)
		}
	}

	if len(results) != len(expectedIDs) {
		t.Fatalf("expected exactly %d hits, got %d", len(expectedIDs), len(results))
	}
}
