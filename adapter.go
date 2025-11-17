package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- Status Store ----

type toolStatusEntry struct {
	LastAdapter        string  `json:"last_adapter"`
	ConsecutiveGeneric int     `json:"consecutive_generic_count"`
	Note               *string `json:"note,omitempty"`
	UpdatedAt          int64   `json:"updated_at"`
}

type statusMap map[string]map[string]*toolStatusEntry // server -> tool -> entry

func loadStatus(path string) (statusMap, error) {
	if path == "" {
		return make(statusMap), nil
	}
	guarded, guardErr := resolveGuardedPath(path)
	if guardErr != nil {
		return make(statusMap), guardErr
	}
	data, err := os.ReadFile(guarded)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(statusMap), nil
		}
		return nil, err
	}
	var out statusMap
	if err := json.Unmarshal(data, &out); err != nil {
		log.Printf("<adapter> status parse error: %v", err)
		return make(statusMap), nil
	}
	if out == nil {
		out = make(statusMap)
	}
	return out, nil
}

func writeStatus(path string, st statusMap) error {
	if path == "" {
		return nil
	}
	guarded, err := resolveGuardedPath(path)
	if err != nil {
		return err
	}
	tmp := guarded + ".tmp"
	data, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, guarded)
}

func setStatus(path, server, tool, adapter string, consecutive int) {
	st, err := loadStatus(path)
	if err != nil {
		log.Printf("<adapter> schema status load error for %s: %v", path, err)
	}
	if _, ok := st[server]; !ok {
		st[server] = make(map[string]*toolStatusEntry)
	}
	st[server][tool] = &toolStatusEntry{
		LastAdapter:        adapter,
		ConsecutiveGeneric: consecutive,
		UpdatedAt:          time.Now().Unix(),
	}
	if err := writeStatus(path, st); err != nil {
		log.Printf("<adapter> schema status write error for %s: %v", path, err)
	}
}

func readStatusEntry(path, server, tool string) *toolStatusEntry {
	st, _ := loadStatus(path)
	if srv, ok := st[server]; ok {
		if e, ok := srv[tool]; ok && e != nil {
			copy := *e
			return &copy
		}
	}
	return nil
}

func logAdoptionTelemetry(server, tool, adapter string, prev *toolStatusEntry, streak int, schema map[string]any) {
	state := "succeeded"
	if adapter == "generic" {
		if prev == nil || prev.LastAdapter != adapter {
			state = "started"
		} else {
			state = "failed"
		}
	} else if prev == nil || prev.LastAdapter != adapter {
		state = "started"
	}
	hash := ""
	if len(schema) > 0 {
		hash = hashSchema(schema)
	}
	log.Printf("<adoption> state=%s server=%s tool=%s adapter=%s streak=%d schema=%s", state, server, tool, adapter, streak, hash)
}

// ---- Override Writer ----

type overrideFile struct {
	SchemaVersion int                              `json:"schemaVersion,omitempty"`
	Tools         map[string]*ToolOverrideConfig   `json:"tools,omitempty"`
	Master        *toolOverrideFragment            `json:"master,omitempty"`
	Servers       map[string]*toolOverrideFragment `json:"servers,omitempty"`
}

func writeServerToolOutputSchema(path, server, tool string, schema map[string]any) error {
	if path == "" {
		return nil
	}
	safePath, err := resolveGuardedPath(path)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(safePath)
	if err != nil {
		return err
	}
	var file overrideFile
	if data, err := os.ReadFile(abs); err == nil {
		_ = json.Unmarshal(data, &file)
	}
	if file.Servers == nil {
		file.Servers = make(map[string]*toolOverrideFragment)
	}
	if file.SchemaVersion < 2 {
		file.SchemaVersion = 2
	}
	frag := file.Servers[server]
	if frag == nil {
		frag = &toolOverrideFragment{}
		file.Servers[server] = frag
	}
	if frag.Tools == nil {
		frag.Tools = make(map[string]*ToolOverrideConfig)
	}
	cfg := frag.Tools[tool]
	if cfg == nil {
		cfg = &ToolOverrideConfig{Enabled: boolPtr(true)}
		frag.Tools[tool] = cfg
	}
	cfg.OutputSchema = copySchemaMap(schema)
	// atomic write
	tmp := abs + ".tmp"
	data, _ := json.MarshalIndent(file, "", "  ")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

func boolPtr(b bool) *bool { return &b }

// ---- Result Adaptation ----

// Returns (modified, adapterUsed, outputSchema, error)
func adaptCallResult(serverName, toolName string, overrides *ToolOverrideSet, manifest *ManifestConfig, payload map[string]any) (bool, string, map[string]any, error) {
	statusPath := ""
	if manifest != nil {
		statusPath = manifest.ToolSchemaStatusPath
	}
	prevStatus := readStatusEntry(statusPath, serverName, toolName)
	res, _ := payload["result"].(map[string]any)
	if res == nil {
		return false, "pass_through", nil, nil
	}
	if sc, ok := res["structuredContent"].(map[string]any); ok && sc != nil {
		// already structured
		setStatus(statusPath, serverName, toolName, "pass_through", 0)
		logAdoptionTelemetry(serverName, toolName, "pass_through", prevStatus, 1, sc)
		return false, "pass_through", sc, nil
	}

	// extract text from content blocks
	text := extractTextContent(res)

	// Declared mapping
	decl := declaredOutputSchema(overrides, serverName, toolName)
	if len(decl) > 0 {
		if field, ok := singleStringField(decl); ok {
			res["structuredContent"] = map[string]any{field: text}
			setStatus(statusPath, serverName, toolName, "declared", 0)
			logAdoptionTelemetry(serverName, toolName, "declared", prevStatus, 1, decl)
			return true, "declared", decl, nil
		}
		if isMetadataContentSchema(decl) {
			mc := parseMetadataContent(text)
			res["structuredContent"] = mc
			setStatus(statusPath, serverName, toolName, "declared", 0)
			logAdoptionTelemetry(serverName, toolName, "declared", prevStatus, 1, decl)
			return true, "declared", decl, nil
		}
	}

	// Generic fallback
	gen := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": map[string]any{"type": "string"},
		},
		"required": []any{"result"},
	}
	res["structuredContent"] = map[string]any{"result": text}
	count := 1
	if prevStatus != nil && prevStatus.LastAdapter == "generic" {
		count = prevStatus.ConsecutiveGeneric + 1
	}
	setStatus(statusPath, serverName, toolName, "generic", count)
	logAdoptionTelemetry(serverName, toolName, "generic", prevStatus, count, gen)
	// persist generic immediately if no declared; else after threshold (2)
	if len(decl) == 0 || count >= 2 {
		_ = writeServerToolOutputSchema(manifest.ToolOverridesPath, serverName, toolName, gen)
	}
	return true, "generic", gen, nil
}

func extractTextContent(result map[string]any) string {
	if result == nil {
		return ""
	}
	if arr, ok := result["content"].([]any); ok {
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if s, _ := m["text"].(string); s != "" {
						return s
					}
				}
			} else if s, ok := item.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func declaredOutputSchema(set *ToolOverrideSet, server, tool string) map[string]any {
	if set == nil {
		return nil
	}
	if frag := set.Servers[server]; frag != nil {
		if cfg := frag.Tools[tool]; cfg != nil && cfg.OutputSchema != nil {
			return copySchemaMap(cfg.OutputSchema)
		}
	}
	if cfg := set.ToolOverrides[tool]; cfg != nil && cfg.OutputSchema != nil {
		return copySchemaMap(cfg.OutputSchema)
	}
	return nil
}

func singleStringField(schema map[string]any) (string, bool) {
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 1 {
		return "", false
	}
	for name, v := range props {
		m, _ := v.(map[string]any)
		if t, _ := m["type"].(string); t == "string" {
			// ensure no extra required fields
			req, _ := schema["required"].([]any)
			ok := true
			for _, r := range req {
				if rs, _ := r.(string); rs != name {
					ok = false
					break
				}
			}
			return name, ok
		}
	}
	return "", false
}

func isMetadataContentSchema(schema map[string]any) bool {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return false
	}
	m, _ := props["metadata"].(map[string]any)
	c, _ := props["content"].(map[string]any)
	if m == nil || c == nil {
		return false
	}
	if t, _ := m["type"].(string); t != "object" {
		return false
	}
	if t, _ := c["type"].(string); t != "string" {
		return false
	}
	return true
}

func parseMetadataContent(text string) map[string]any {
	res := map[string]any{
		"metadata": map[string]any{
			"adapter": "declared:metadata-content",
		},
		"content": text,
	}
	if len(text) >= 9 && text[:9] == "METADATA:" {
		body := text[9:]
		if i := indexDoubleNewline(body); i >= 0 {
			meta := body[:i]
			content := body[i+2:]
			var m map[string]any
			if err := json.Unmarshal([]byte(stringsTrim(meta)), &m); err == nil {
				res["metadata"] = m
			}
			res["content"] = stringsTrim(content)
		}
	}
	return res
}

func indexDoubleNewline(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '\n' && s[i+1] == '\n' {
			return i
		}
	}
	return -1
}

func stringsTrim(s string) string { return strings.TrimSpace(s) }
