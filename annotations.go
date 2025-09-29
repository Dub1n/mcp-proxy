package main

import "github.com/mark3labs/mcp-go/mcp"

func normalizeToolAnnotations(tool mcp.Tool) map[string]any {
	annotations := make(map[string]any, 5)
	existing := tool.Annotations

	if existing.Title != "" {
		annotations["title"] = existing.Title
	}

	if existing.ReadOnlyHint != nil {
		annotations["readOnlyHint"] = *existing.ReadOnlyHint
	} else {
		annotations["readOnlyHint"] = false
	}

	if existing.DestructiveHint != nil {
		annotations["destructiveHint"] = *existing.DestructiveHint
	} else {
		annotations["destructiveHint"] = false
	}

	if existing.IdempotentHint != nil {
		annotations["idempotentHint"] = *existing.IdempotentHint
	} else {
		annotations["idempotentHint"] = false
	}

	if existing.OpenWorldHint != nil {
		annotations["openWorldHint"] = *existing.OpenWorldHint
	} else {
		annotations["openWorldHint"] = false
	}

	return annotations
}
