package main

import "testing"

import "github.com/mark3labs/mcp-go/mcp"

func TestNormalizeToolAnnotationsDefaults(t *testing.T) {
	tool := mcp.Tool{Name: "example"}

	annotations := normalizeToolAnnotations(tool)

	if v, ok := annotations["readOnlyHint"].(bool); !ok || v {
		t.Fatalf("expected readOnlyHint=false, got %v", annotations["readOnlyHint"])
	}
	if v, ok := annotations["destructiveHint"].(bool); !ok || v {
		t.Fatalf("expected destructiveHint=false, got %v", annotations["destructiveHint"])
	}
	if v, ok := annotations["idempotentHint"].(bool); !ok || v {
		t.Fatalf("expected idempotentHint=false, got %v", annotations["idempotentHint"])
	}
	if v, ok := annotations["openWorldHint"].(bool); !ok || v {
		t.Fatalf("expected openWorldHint=false, got %v", annotations["openWorldHint"])
	}
}

func TestNormalizeToolAnnotationsPreservesExisting(t *testing.T) {
	trueVal := true
	falseVal := false
	tool := mcp.Tool{
		Name: "example",
		Annotations: mcp.ToolAnnotation{
			Title:           "My Tool",
			ReadOnlyHint:    &trueVal,
			DestructiveHint: &falseVal,
		},
	}

	annotations := normalizeToolAnnotations(tool)

	if annotations["title"] != "My Tool" {
		t.Fatalf("expected title preserved, got %v", annotations["title"])
	}
	if v, ok := annotations["readOnlyHint"].(bool); !ok || !v {
		t.Fatalf("expected readOnlyHint=true, got %v", annotations["readOnlyHint"])
	}
	if v, ok := annotations["destructiveHint"].(bool); !ok || v {
		t.Fatalf("expected destructiveHint=false, got %v", annotations["destructiveHint"])
	}
}
