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
	LastAdapter            string  `json:"last_adapter"`
	ConsecutiveGeneric     int     `json:"consecutive_generic_count"`
	Note                   *string `json:"note,omitempty"`
	UpdatedAt              int64   `json:"updated_at"`
}

type statusMap map[string]map[string]*toolStatusEntry // server -> tool -> entry

func loadStatus(path string) (statusMap, error) {
	if path == "" {
		return make(statusMap), nil
	}
	data, err := os.ReadFile(path)
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
	tmp := path + ".tmp"
	data, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func setStatus(path, server, tool, adapter string, consecutive int) {
	st, _ := loadStatus(path)
	if _, ok := st[server]; !ok {
		st[server] = make(map[string]*toolStatusEntry)
	}
	st[server][tool] = &toolStatusEntry{
		LastAdapter:        adapter,
		ConsecutiveGeneric: consecutive,
		UpdatedAt:          time.Now().Unix(),
	}
	_ = writeStatus(path, st)
}

func getConsecutiveGeneric(path, server, tool string) int {
	st, _ := loadStatus(path)
	if srv, ok := st[server]; ok {
		if e, ok := srv[tool]; ok && e != nil {
			return e.ConsecutiveGeneric
		}
	}
	return 0
}

// ---- Override Writer ----

type overrideFile struct {
	Tools   map[string]*ToolOverrideConfig   `json:"tools,omitempty"`
	Master  *toolOverrideFragment            `json:"master,omitempty"`
	Servers map[string]*toolOverrideFragment `json:"servers,omitempty"`
}

func writeServerToolOutputSchema(path, server, tool string, schema map[string]any) error {
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
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
	res, _ := payload["result"].(map[string]any)
	if res == nil {
		return false, "pass_through", nil, nil
	}
	if sc, ok := res["structuredContent"].(map[string]any); ok && sc != nil {
		// already structured
		setStatus(manifest.ToolSchemaStatusPath, serverName, toolName, "pass_through", 0)
		return false, "pass_through", sc, nil
	}

	// extract text from content blocks
	text := extractTextContent(res)

	// Declared mapping
	decl := declaredOutputSchema(overrides, serverName, toolName)
	if len(decl) > 0 {
		if field, ok := singleStringField(decl); ok {
			res["structuredContent"] = map[string]any{field: text}
			setStatus(manifest.ToolSchemaStatusPath, serverName, toolName, "declared", 0)
			return true, "declared", decl, nil
		}
		if isMetadataContentSchema(decl) {
			mc := parseMetadataContent(text)
			res["structuredContent"] = mc
			setStatus(manifest.ToolSchemaStatusPath, serverName, toolName, "declared", 0)
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
	count := getConsecutiveGeneric(manifest.ToolSchemaStatusPath, serverName, toolName) + 1
	setStatus(manifest.ToolSchemaStatusPath, serverName, toolName, "generic", count)
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
