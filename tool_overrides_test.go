package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadToolOverridesFromPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.json")
	content := `{
	        "servers": {
	            "fs": {
	                "tools": {
	                    "read_file": {
	                        "annotations": {
	                            "readOnlyHint": true
	                        }
	                    }
	                }
	            }
	        },
	        "master": {
	            "tools": {
	                "*": {
	                    "annotations": {
	                        "openWorldHint": true
	                    }
	                }
	            }
	        }
	    }`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write overrides file: %v", err)
	}

	set, err := loadToolOverridesFromPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if set == nil {
		t.Fatalf("expected overrides map")
	}
	if set.ToolOverrides["*"] == nil {
		t.Fatalf("expected master override entry present")
	}
	descriptor := map[string]any{"annotations": map[string]any{}}
	descriptor = applyToolOverride("read_file", descriptor, set)
	ann, _ := descriptor["annotations"].(map[string]any)
	if ann == nil {
		t.Fatalf("expected annotations map after override")
	}
	if v, ok := ann["readOnlyHint"].(bool); !ok || !v {
		t.Fatalf("expected readOnlyHint true after overrides, got %v", ann["readOnlyHint"])
	}
	if v, ok := ann["openWorldHint"].(bool); !ok || !v {
		t.Fatalf("expected master openWorldHint true, got %v", ann["openWorldHint"])
	}
}

func TestMergeToolOverrideMaps(t *testing.T) {
	trueVal := true
	base := map[string]*ToolOverrideConfig{
		"read_file": {Annotations: &AnnotationOverrideConfig{ReadOnlyHint: &trueVal}},
	}
	falseVal := false
	extra := map[string]*ToolOverrideConfig{
		"read_file":  {Annotations: &AnnotationOverrideConfig{DestructiveHint: &falseVal}},
		"write_file": {Annotations: &AnnotationOverrideConfig{DestructiveHint: &trueVal}},
	}

	merged := mergeToolOverrideMaps(base, extra)
	if len(merged) != 2 {
		t.Fatalf("expected 2 overrides, got %d", len(merged))
	}
	rf := merged["read_file"]
	if rf == nil || rf.Annotations == nil {
		t.Fatalf("expected merged read_file annotations")
	}
	if rf.Annotations.ReadOnlyHint == nil || !*rf.Annotations.ReadOnlyHint {
		t.Fatalf("expected readOnlyHint to remain true")
	}
	if rf.Annotations.DestructiveHint == nil || *rf.Annotations.DestructiveHint {
		t.Fatalf("expected destructiveHint to be false")
	}
}
