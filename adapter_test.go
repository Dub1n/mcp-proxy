package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newManifestForTest(statusPath, overridesPath string) *ManifestConfig {
	return &ManifestConfig{
		ToolOverridesPath:    overridesPath,
		ToolSchemaStatusPath: statusPath,
	}
}

func testHomes(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("STELAE_STATE_HOME", base)
	t.Setenv("STELAE_CONFIG_HOME", base)
	return base
}

func resultWithText(text string) map[string]any {
	return map[string]any{
		"result": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": text},
			},
		},
	}
}

func resultWithStructured(structured map[string]any) map[string]any {
	return map[string]any{
		"result": map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": ""}},
			"structuredContent": structured,
		},
	}
}

func overridesWithSingleString(server, tool, field string) *ToolOverrideSet {
	set := &ToolOverrideSet{Servers: map[string]*toolOverrideFragment{}}
	frag := &toolOverrideFragment{Tools: map[string]*ToolOverrideConfig{}}
	frag.Tools[tool] = &ToolOverrideConfig{OutputSchema: map[string]any{
		"type":       "object",
		"properties": map[string]any{field: map[string]any{"type": "string"}},
		"required":   []any{field},
	}}
	set.Servers[server] = frag
	set.ToolOverrides = make(map[string]*ToolOverrideConfig)
	return set
}

func overridesWithMetadataContent(server, tool string) *ToolOverrideSet {
	set := &ToolOverrideSet{Servers: map[string]*toolOverrideFragment{}}
	frag := &toolOverrideFragment{Tools: map[string]*ToolOverrideConfig{}}
	frag.Tools[tool] = &ToolOverrideConfig{OutputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"metadata": map[string]any{"type": "object"},
			"content":  map[string]any{"type": "string"},
		},
		"required": []any{"metadata", "content"},
	}}
	set.Servers[server] = frag
	set.ToolOverrides = make(map[string]*ToolOverrideConfig)
	return set
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse json %s: %v", path, err)
	}
}

func TestAdapt_PassThrough_NoChange(t *testing.T) {
	base := testHomes(t)
	statusPath := filepath.Join(base, "status.json")
	overridesPath := filepath.Join(base, "overrides.json")
	manifest := newManifestForTest(statusPath, overridesPath)

	payload := resultWithStructured(map[string]any{"ok": true})
	modified, used, _, err := adaptCallResult("srv", "tool", nil, manifest, payload)
	if err != nil {
		t.Fatalf("adaptCallResult error: %v", err)
	}
	if modified {
		t.Fatalf("expected unmodified for pass-through")
	}
	if used != "pass_through" {
		t.Fatalf("expected pass_through, got %s", used)
	}
}

func TestAdapt_Declared_SingleString_WrapsText(t *testing.T) {
	base := testHomes(t)
	statusPath := filepath.Join(base, "status.json")
	overridesPath := filepath.Join(base, "overrides.json")
	manifest := newManifestForTest(statusPath, overridesPath)

	overrides := overridesWithSingleString("srv", "tool", "result")
	payload := resultWithText("hello world")

	modified, used, schema, err := adaptCallResult("srv", "tool", overrides, manifest, payload)
	if err != nil {
		t.Fatalf("adaptCallResult error: %v", err)
	}
	if !modified || used != "declared" {
		t.Fatalf("expected declared modification, used=%s modified=%v", used, modified)
	}
	res := payload["result"].(map[string]any)
	sc := res["structuredContent"].(map[string]any)
	if sc["result"] != "hello world" {
		t.Fatalf("expected wrapped result, got %#v", sc)
	}
	if schema == nil || schema["type"].(string) != "object" {
		t.Fatalf("expected non-nil declared schema")
	}
}

func TestAdapt_Generic_PersistsOverrideAndStatus(t *testing.T) {
	base := testHomes(t)
	statusPath := filepath.Join(base, "status.json")
	overridesPath := filepath.Join(base, "overrides.json")
	manifest := newManifestForTest(statusPath, overridesPath)

	payload := resultWithText("text only")

	modified, used, schema, err := adaptCallResult("srv", "plain", &ToolOverrideSet{Servers: map[string]*toolOverrideFragment{}, ToolOverrides: map[string]*ToolOverrideConfig{}}, manifest, payload)
	if err != nil {
		t.Fatalf("adaptCallResult error: %v", err)
	}
	if !modified || used != "generic" {
		t.Fatalf("expected generic modification, used=%s modified=%v", used, modified)
	}
	// status should show consecutive_generic_count >= 1
	var status map[string]map[string]map[string]any
	readJSON(t, statusPath, &status)
	if status["srv"]["plain"]["consecutive_generic_count"].(float64) < 1 {
		t.Fatalf("expected consecutive_generic_count >= 1, got %#v", status)
	}
	// overrides should have persisted schema under servers.srv.tools.plain.outputSchema
	var overridesFile map[string]any
	readJSON(t, overridesPath, &overridesFile)
	servers := overridesFile["servers"].(map[string]any)
	srv := servers["srv"].(map[string]any)
	tools := srv["tools"].(map[string]any)
	cfg := tools["plain"].(map[string]any)
	out := cfg["outputSchema"].(map[string]any)
	if out["type"] != "object" {
		t.Fatalf("expected outputSchema type object, got %#v", out)
	}
	// also verify payload structuredContent present
	res := payload["result"].(map[string]any)
	sc := res["structuredContent"].(map[string]any)
	if sc["result"] != "text only" {
		t.Fatalf("expected generic wrapped content, got %#v", sc)
	}
	if schema == nil {
		t.Fatalf("expected returned schema for generic path")
	}
}
