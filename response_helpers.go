package main

import "github.com/mark3labs/mcp-go/mcp"

func collectTools(servers map[string]*Server) []map[string]any {
	var searchDescriptor map[string]any
	var fetchDescriptor map[string]any

	for _, srv := range servers {
		for _, tool := range srv.tools {
			switch tool.Name {
			case facadeSearchToolName:
				if searchDescriptor == nil {
					searchDescriptor = mergeWithFacadeDefaults(toolDescriptorFromServer(tool), searchToolDescriptor())
				}
			case facadeFetchToolName:
				if fetchDescriptor == nil {
					fetchDescriptor = mergeWithFacadeDefaults(toolDescriptorFromServer(tool), fetchToolDescriptor())
				}
			}
		}
	}

	if searchDescriptor == nil {
		searchDescriptor = searchToolDescriptor()
	}
	if fetchDescriptor == nil {
		fetchDescriptor = fetchToolDescriptor()
	}

	return []map[string]any{searchDescriptor, fetchDescriptor}
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
				"url": map[string]any{
					"title": "Url",
					"type":  "string",
				},
			},
			"required": []string{"url"},
		},
	}
}

func searchManifestDescriptor() map[string]any {
	return searchToolDescriptor()
}

func fetchManifestDescriptor() map[string]any {
	return fetchToolDescriptor()
}
