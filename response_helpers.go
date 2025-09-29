package main

import (
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
)

func collectTools(servers map[string]*Server) []map[string]any {
	seen := make(map[string]map[string]any)
	for _, srv := range servers {
		for _, tool := range srv.tools {
			descriptor := toolDescriptorFromServer(tool)
			if tool.Name == facadeSearchToolName {
				descriptor = ensureSearchDescriptor(descriptor)
			} else if tool.Name == facadeFetchToolName {
				descriptor = ensureFetchDescriptor(descriptor)
			}
			if descriptor == nil {
				continue
			}
			if _, exists := seen[tool.Name]; !exists {
				seen[tool.Name] = descriptor
			}
		}
	}

	if _, ok := seen[facadeSearchToolName]; !ok {
		seen[facadeSearchToolName] = ensureSearchDescriptor(nil)
	}
	if _, ok := seen[facadeFetchToolName]; !ok {
		seen[facadeFetchToolName] = ensureFetchDescriptor(nil)
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		result = append(result, seen[name])
	}
	return result
}

func toolDescriptorFromServer(tool mcp.Tool) map[string]any {
	descriptor := map[string]any{
		"name": tool.Name,
	}
	if tool.Description != "" {
		descriptor["description"] = tool.Description
	}
	if len(tool.RawInputSchema) > 0 {
		descriptor["inputSchema"] = tool.RawInputSchema
	} else if tool.InputSchema.Type != "" || len(tool.InputSchema.Properties) > 0 || len(tool.InputSchema.Required) > 0 || len(tool.InputSchema.Defs) > 0 {
		descriptor["inputSchema"] = tool.InputSchema
	}
	if len(tool.RawOutputSchema) > 0 {
		descriptor["outputSchema"] = tool.RawOutputSchema
	} else if tool.OutputSchema.Type != "" || len(tool.OutputSchema.Properties) > 0 || len(tool.OutputSchema.Required) > 0 || len(tool.OutputSchema.Defs) > 0 {
		descriptor["outputSchema"] = tool.OutputSchema
	}
	if tool.Annotations != (mcp.ToolAnnotation{}) {
		descriptor["annotations"] = tool.Annotations
	}
	return descriptor
}

func mergeWithFacadeDefaults(base map[string]any, fallback map[string]any) map[string]any {
	if base == nil {
		return fallback
	}
	merged := make(map[string]any, len(fallback)+len(base))
	for k, v := range fallback {
		merged[k] = v
	}
	for k, v := range base {
		if v == nil {
			continue
		}
		if str, ok := v.(string); ok && str == "" {
			continue
		}
		merged[k] = v
	}
	return merged
}

func collectPrompts(servers map[string]*Server) []map[string]any {
	prompts := make([]map[string]any, 0)
	for _, srv := range servers {
		for _, prompt := range srv.prompts {
			item := map[string]any{"name": prompt.Name}
			if prompt.Description != "" {
				item["description"] = prompt.Description
			}
			if len(prompt.Arguments) > 0 {
				item["arguments"] = prompt.Arguments
			}
			prompts = append(prompts, item)
		}
	}
	return prompts
}

func collectResources(servers map[string]*Server) []map[string]any {
	resources := make([]map[string]any, 0)
	for _, srv := range servers {
		for _, resource := range srv.resources {
			item := map[string]any{
				"uri":  resource.URI,
				"name": resource.Name,
			}
			if resource.Description != "" {
				item["description"] = resource.Description
			}
			if resource.MIMEType != "" {
				item["mimeType"] = resource.MIMEType
			}
			resources = append(resources, item)
		}
	}
	return resources
}

func collectResourceTemplates(servers map[string]*Server) []map[string]any {
	templates := make([]map[string]any, 0)
	for _, srv := range servers {
		for _, tpl := range srv.resourceTemplates {
			item := map[string]any{
				"name": tpl.Name,
			}
			if tpl.Description != "" {
				item["description"] = tpl.Description
			}
			if tpl.MIMEType != "" {
				item["mimeType"] = tpl.MIMEType
			}
			if tpl.URITemplate != nil {
				item["uriTemplate"] = tpl.URITemplate
			}
			templates = append(templates, item)
		}
	}
	return templates
}

func buildInitializeResult(config *Config, servers map[string]*Server) map[string]any {
	tools := collectTools(servers)
	prompts := collectPrompts(servers)
	resources := collectResources(servers)
	resourceTemplates := collectResourceTemplates(servers)

	capabilities := map[string]any{}
	if len(tools) > 0 {
		capabilities["tools"] = map[string]any{"listChanged": false}
	}
	if len(prompts) > 0 {
		capabilities["prompts"] = map[string]any{"listChanged": false}
	}
	if len(resources) > 0 || len(resourceTemplates) > 0 {
		capabilities["resources"] = map[string]any{"subscribe": false, "listChanged": false}
	}

	serverInfo := map[string]any{
		"name":    "",
		"version": "",
	}
	if config != nil && config.McpProxy != nil {
		serverInfo["name"] = config.McpProxy.Name
		serverInfo["version"] = config.McpProxy.Version
	}

	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"serverInfo":      serverInfo,
		"capabilities":    capabilities,
		"tools":           tools,
	}
	if len(prompts) > 0 {
		result["prompts"] = prompts
	}
	if len(resources) > 0 {
		result["resources"] = resources
	}
	if len(resourceTemplates) > 0 {
		result["resourceTemplates"] = resourceTemplates
	}
	return result
}

const (
	facadeSearchToolName = "search"
	facadeFetchToolName  = "fetch"
)

func searchToolDescriptor() map[string]any {
	return map[string]any{
		"name":        facadeSearchToolName,
		"description": "Lightweight search placeholder exposed for ChatGPT connector verification.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"title": "Query",
					"type":  "string",
				},
			},
			"required": []string{"query"},
		},
	}
}

func fetchToolDescriptor() map[string]any {
	return map[string]any{
		"name":        facadeFetchToolName,
		"description": "Connector-compliant fetch placeholder used when no upstream descriptor is available.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"title": "Id",
					"type":  "string",
				},
			},
			"required": []string{"id"},
		},
	}
}

func ensureSearchDescriptor(descriptor map[string]any) map[string]any {
	base := mergeWithFacadeDefaults(descriptor, searchToolDescriptor())
	schema, _ := base["inputSchema"].(map[string]any)
	if schema == nil {
		base["inputSchema"] = searchToolDescriptor()["inputSchema"]
		return base
	}
	props := ensurePropertiesMap(schema)
	fallbackProps, _ := searchToolDescriptor()["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if fallbackProps != nil {
		if _, ok := props["query"]; !ok {
			props["query"] = fallbackProps["query"]
		}
	}
	ensureRequiredField(schema, "query")
	return base
}

func ensureFetchDescriptor(descriptor map[string]any) map[string]any {
	base := mergeWithFacadeDefaults(descriptor, fetchToolDescriptor())
	schema, _ := base["inputSchema"].(map[string]any)
	if schema == nil {
		base["inputSchema"] = fetchToolDescriptor()["inputSchema"]
		return base
	}
	props := ensurePropertiesMap(schema)
	fallbackSchema, _ := fetchToolDescriptor()["inputSchema"].(map[string]any)
	if fallbackProps, ok := fallbackSchema["properties"].(map[string]any); ok {
		props["id"] = fallbackProps["id"]
	}
	removeRequiredField(schema, "url")
	ensureRequiredField(schema, "id")
	return base
}

func ensurePropertiesMap(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
		schema["properties"] = props
	}
	return props
}

func ensureRequiredField(schema map[string]any, field string) {
	if field == "" || schema == nil {
		return
	}
	var existing []string
	if raw, ok := schema["required"]; ok {
		switch v := raw.(type) {
		case []string:
			existing = append(existing, v...)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					existing = append(existing, s)
				}
			}
		}
	}
	for _, item := range existing {
		if item == field {
			out := make([]any, len(existing))
			for i, val := range existing {
				out[i] = val
			}
			schema["required"] = out
			return
		}
	}
	existing = append(existing, field)
	out := make([]any, len(existing))
	for i, val := range existing {
		out[i] = val
	}
	schema["required"] = out
}

func removeRequiredField(schema map[string]any, field string) {
	if field == "" || schema == nil {
		return
	}
	var existing []string
	if raw, ok := schema["required"]; ok {
		switch v := raw.(type) {
		case []string:
			existing = append(existing, v...)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					existing = append(existing, s)
				}
			}
		}
	}
	if len(existing) == 0 {
		return
	}
	filtered := make([]string, 0, len(existing))
	for _, item := range existing {
		if item != field {
			filtered = append(filtered, item)
		}
	}
	out := make([]any, len(filtered))
	for i, val := range filtered {
		out[i] = val
	}
	schema["required"] = out
}

func searchManifestDescriptor() map[string]any {
	return searchToolDescriptor()
}

func fetchManifestDescriptor() map[string]any {
	return fetchToolDescriptor()
}
