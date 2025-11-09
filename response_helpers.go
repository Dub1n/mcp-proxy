package main

import (
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
)

type aggregatedTool struct {
	descriptor map[string]any
	servers    map[string]struct{}
}

func newAggregatedTool(descriptor map[string]any) *aggregatedTool {
	return &aggregatedTool{descriptor: descriptor, servers: make(map[string]struct{})}
}

func (a *aggregatedTool) addServer(name string) {
	if name == "" {
		return
	}
	a.servers[name] = struct{}{}
}

func (a *aggregatedTool) serverList() []string {
	if len(a.servers) == 0 {
		return nil
	}
	list := make([]string, 0, len(a.servers))
	for name := range a.servers {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

func collectTools(servers map[string]*Server, overrides *ToolOverrideSet) []map[string]any {
	seen := make(map[string]*aggregatedTool)
	for serverName, srv := range servers {
		if !serverEnabled(overrides, serverName) {
			continue
		}
		for _, tool := range srv.tools {
			if !toolEnabled(overrides, serverName, tool.Name) {
				continue
			}
			descriptor := toolDescriptorFromServer(tool)
			if tool.Name == facadeSearchToolName {
				descriptor = ensureSearchDescriptor(descriptor)
			} else if tool.Name == facadeFetchToolName {
				descriptor = ensureFetchDescriptor(descriptor)
			}
			if descriptor == nil {
				continue
			}
			entry, exists := seen[tool.Name]
			if exists {
				entry.descriptor = mergeToolDescriptors(entry.descriptor, descriptor)
				entry.addServer(serverName)
			} else {
				copyDescriptor := descriptor
				entry = newAggregatedTool(copyDescriptor)
				entry.addServer(serverName)
				seen[tool.Name] = entry
			}
		}
	}

	if _, ok := seen[facadeSearchToolName]; !ok && toolEnabled(overrides, "facade", facadeSearchToolName) {
		entry := newAggregatedTool(ensureSearchDescriptor(nil))
		entry.addServer("facade")
		seen[facadeSearchToolName] = entry
	}
	if _, ok := seen[facadeFetchToolName]; !ok && toolEnabled(overrides, "facade", facadeFetchToolName) {
		entry := newAggregatedTool(ensureFetchDescriptor(nil))
		entry.addServer("facade")
		seen[facadeFetchToolName] = entry
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		entry := seen[name]
		descriptor := applyToolOverride(name, entry.descriptor, overrides)
		descriptor = attachStelaeMetadata(descriptor, entry.serverList())
		result = append(result, descriptor)
	}
	return result
}

func attachStelaeMetadata(descriptor map[string]any, servers []string) map[string]any {
	if descriptor == nil || len(servers) == 0 {
		return descriptor
	}
	meta := map[string]any{
		"servers":       servers,
		"primaryServer": servers[0],
	}
	if existing, ok := descriptor["x-stelae"].(map[string]any); ok && existing != nil {
		for k, v := range meta {
			existing[k] = v
		}
		return descriptor
	}
	descriptor["x-stelae"] = meta
	return descriptor
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
	descriptor["annotations"] = normalizeToolAnnotations(tool)
	return descriptor
}

func mergeToolDescriptors(existing, candidate map[string]any) map[string]any {
	if existing == nil {
		return candidate
	}
	if candidate == nil {
		return existing
	}
	merged := make(map[string]any, len(existing)+len(candidate))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range candidate {
		if k == "annotations" {
			merged[k] = mergeAnnotations(merged[k], v)
			continue
		}
		if isEmptyValue(merged[k]) && !isEmptyValue(v) {
			merged[k] = v
		}
	}
	return merged
}

func mergeAnnotations(existing, candidate any) map[string]any {
	var base map[string]any
	if existingMap, ok := existing.(map[string]any); ok && existingMap != nil {
		base = copyStringAnyMap(existingMap)
	} else {
		base = make(map[string]any)
	}
	candidateMap, ok := candidate.(map[string]any)
	if !ok || candidateMap == nil {
		return base
	}
	for key, val := range candidateMap {
		if boolVal, ok := toBool(val); ok {
			if boolVal {
				base[key] = true
			} else if _, exists := base[key]; !exists {
				base[key] = false
			}
			continue
		}
		if _, exists := base[key]; !exists || base[key] == nil {
			base[key] = val
		}
	}
	return base
}

func applyToolOverride(name string, descriptor map[string]any, set *ToolOverrideSet) map[string]any {
	if descriptor == nil || set == nil {
		return descriptor
	}
	if master := set.ToolOverrides["*"]; master != nil {
		descriptor = applySingleOverride(descriptor, master, false)
	}
	if override := set.ToolOverrides[name]; override != nil {
		descriptor = applySingleOverride(descriptor, override, true)
	}
	return descriptor
}

func applySingleOverride(descriptor map[string]any, override *ToolOverrideConfig, allowRename bool) map[string]any {
	if descriptor == nil || override == nil {
		return descriptor
	}
	if override.Annotations != nil {
		descriptor["annotations"] = applyAnnotationOverride(descriptor["annotations"], override.Annotations)
	}
	if override.Description != nil {
		descriptor["description"] = *override.Description
	}
	if allowRename && override.Name != nil {
		descriptor["name"] = *override.Name
	}
	if override.InputSchema != nil {
		descriptor["inputSchema"] = copySchemaMap(override.InputSchema)
	}
	if override.OutputSchema != nil {
		descriptor["outputSchema"] = copySchemaMap(override.OutputSchema)
	}
	return descriptor
}

func applyAnnotationOverride(existing any, override *AnnotationOverrideConfig) map[string]any {
	annotations, _ := existing.(map[string]any)
	if annotations == nil {
		annotations = make(map[string]any)
	}
	if override.Title != nil {
		annotations["title"] = *override.Title
	}
	if override.ReadOnlyHint != nil {
		annotations["readOnlyHint"] = *override.ReadOnlyHint
	}
	if override.DestructiveHint != nil {
		annotations["destructiveHint"] = *override.DestructiveHint
	}
	if override.IdempotentHint != nil {
		annotations["idempotentHint"] = *override.IdempotentHint
	}
	if override.OpenWorldHint != nil {
		annotations["openWorldHint"] = *override.OpenWorldHint
	}
	return annotations
}

func serverEnabled(set *ToolOverrideSet, serverName string) bool {
	if set == nil {
		return true
	}
	enabled := true
	if set.Master != nil && set.Master.Enabled != nil {
		enabled = *set.Master.Enabled
	}
	if fragment := set.Servers[serverName]; fragment != nil && fragment.Enabled != nil {
		enabled = *fragment.Enabled
	}
	return enabled
}

func copyStringAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func isEmptyValue(v any) bool {
	switch val := v.(type) {
	case nil:
		return true
	case string:
		return val == ""
	}
	return false
}

func toBool(v any) (bool, bool) {
	switch b := v.(type) {
	case bool:
		return b, true
	case *bool:
		if b == nil {
			return false, false
		}
		return *b, true
	default:
		return false, false
	}
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

func buildInitializeResult(config *Config, servers map[string]*Server, overrides *ToolOverrideSet) map[string]any {
	tools := collectTools(servers, overrides)
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
	if schema == nil {
		return
	}
	raw, _ := schema["required"].([]any)
	for _, item := range raw {
		if str, ok := item.(string); ok && str == field {
			return
		}
	}
	schema["required"] = append(raw, field)
}

func removeRequiredField(schema map[string]any, field string) {
	if schema == nil {
		return
	}
	raw, _ := schema["required"].([]any)
	if len(raw) == 0 {
		return
	}
	filtered := make([]any, 0, len(raw))
	for _, item := range raw {
		if str, ok := item.(string); ok && str == field {
			continue
		}
		filtered = append(filtered, item)
	}
	schema["required"] = filtered
}

func searchManifestDescriptor() map[string]any {
	return searchToolDescriptor()
}

func fetchManifestDescriptor() map[string]any {
	return fetchToolDescriptor()
}
