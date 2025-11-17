package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type catalogFile struct {
	Path        string
	LoadedAt    time.Time
	GeneratedAt time.Time
	ToolsByName map[string]map[string]any
	Raw         map[string]any
}

type liveSnapshotState struct {
	liveCatalog         map[string]any
	liveCatalogPath     string
	liveDescriptors     map[string]any
	liveDescriptorsPath string
}

func loadCatalogFile(path string) (*catalogFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	tools := parseToolSlice(raw["tools"])
	if len(tools) == 0 {
		return nil, errors.New("catalog contains no tools")
	}
	toolsByName := make(map[string]map[string]any, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		toolsByName[name] = tool
	}
	loaded := &catalogFile{
		Path:        path,
		LoadedAt:    time.Now().UTC(),
		ToolsByName: toolsByName,
		Raw:         raw,
	}
	if ts, ok := raw["generatedAt"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			loaded.GeneratedAt = parsed
		}
	}
	return loaded, nil
}

func parseToolSlice(val any) []map[string]any {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, raw := range v {
			if m, ok := raw.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func writeSnapshotWithHistory(home, basePath string, payload any, historyCount int, stamp time.Time) (string, error) {
	if stamp.IsZero() {
		stamp = time.Now().UTC()
	}
	resolvedBase, err := mkdirAllUnder(home, basePath)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := writeAtomic(resolvedBase, data); err != nil {
		return "", err
	}
	if historyCount > 0 {
		ts := stamp.UTC().Format("20060102-150405")
		stamped := fmt.Sprintf("%s.%s.json", strings.TrimSuffix(resolvedBase, ".json"), ts)
		if stampedPath, err := mkdirAllUnder(home, stamped); err == nil {
			_ = writeAtomic(stampedPath, data)
		}
		_ = pruneHistory(resolvedBase, historyCount)
	}
	return resolvedBase, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func pruneHistory(basePath string, keep int) error {
	if keep < 0 {
		return nil
	}
	dir := filepath.Dir(basePath)
	prefix := strings.TrimSuffix(filepath.Base(basePath), ".json") + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	history := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		full := filepath.Join(dir, name)
		if full == basePath {
			continue
		}
		history = append(history, full)
	}
	if len(history) <= keep {
		return nil
	}
	sort.Strings(history)
	for i := 0; i < len(history)-keep; i++ {
		_ = os.Remove(history[i])
	}
	return nil
}

func collectLiveDescriptors(servers map[string]*Server) []map[string]any {
	seen := make(map[string]*aggregatedTool)
	for serverName, srv := range servers {
		for _, tool := range srv.tools {
			descriptor := toolDescriptorFromServer(tool)
			entry, exists := seen[tool.Name]
			if exists {
				entry.descriptor = mergeToolDescriptors(entry.descriptor, descriptor)
				entry.addServer(serverName)
			} else {
				entry = newAggregatedTool(descriptor)
				entry.addServer(serverName)
				seen[tool.Name] = entry
			}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		entry := seen[name]
		record := copyStringAnyMap(entry.descriptor)
		if record == nil {
			record = make(map[string]any)
		}
		record["name"] = name
		record["servers"] = entry.serverList()
		if hash := hashSchema(record); hash != "" {
			record["schemaHash"] = hash
		}
		result = append(result, record)
	}
	return result
}

func hashSchema(record map[string]any) string {
	data, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func buildLiveCatalogSnapshot(config *Config, servers map[string]*Server, overrides *ToolOverrideSet, intended *catalogFile, generatedAt time.Time) map[string]any {
	snapshot := buildInitializeResult(config, servers, overrides, intended)
	snapshot["generatedAt"] = generatedAt.UTC().Format(time.RFC3339Nano)
	return snapshot
}

func buildLiveDescriptorSnapshot(servers map[string]*Server, generatedAt time.Time) map[string]any {
	return map[string]any{
		"generatedAt": generatedAt.UTC().Format(time.RFC3339Nano),
		"tools":       collectLiveDescriptors(servers),
	}
}
