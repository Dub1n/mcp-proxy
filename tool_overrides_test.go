package main

import (
	"os"
	"path/filepath"
	"strings"
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
	                        "name": "fs_read_file",
	                        "description": "Read with override",
	                        "annotations": {
	                            "readOnlyHint": true,
	                            "title": "FS Reader"
	                        }
	                    }
	                }
	            }
	        },
	        "master": {
	            "tools": {
	                "*": {
	                    "name": "global_alias",
	                    "description": "Master description",
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
	if set.ToolOverrides["*"].Name != nil {
		t.Fatalf("expected master rename to be stripped, got %q", *set.ToolOverrides["*"].Name)
	}
	if set.ToolOverrides["*"].Description == nil {
		t.Fatalf("expected master description preserved")
	}
	if len(set.Warnings) < 2 {
		t.Fatalf("expected warnings for master overrides, got %#v", set.Warnings)
	}
	foundRename := false
	foundDescription := false
	for _, msg := range set.Warnings {
		if strings.Contains(msg, "cannot rename") {
			foundRename = true
		}
		if strings.Contains(msg, "description override") {
			foundDescription = true
		}
	}
	if !foundRename {
		t.Fatalf("expected rename warning in %#v", set.Warnings)
	}
	if !foundDescription {
		t.Fatalf("expected description warning in %#v", set.Warnings)
	}
	if alias, ok := set.AliasForTool("read_file"); !ok || alias != "fs_read_file" {
		t.Fatalf("expected alias mapping for read_file, got %q", alias)
	}
	if original, ok := set.OriginalForAlias("fs_read_file"); !ok || original != "read_file" {
		t.Fatalf("expected reverse alias mapping, got %q", original)
	}
	if renamed := set.Renamed["read_file"]; renamed != "fs_read_file" {
		t.Fatalf("expected renamed map entry, got %q", renamed)
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
	if title, ok := ann["title"].(string); !ok || title != "FS Reader" {
		t.Fatalf("expected title override, got %v", ann["title"])
	}
	if desc, ok := descriptor["description"].(string); !ok || desc != "Read with override" {
		t.Fatalf("expected description override, got %v", descriptor["description"])
	}
	if name, ok := descriptor["name"].(string); !ok || name != "fs_read_file" {
		t.Fatalf("expected name override to apply, got %v", descriptor["name"])
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
