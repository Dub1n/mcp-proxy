package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type toolOverrideFile struct {
	Tools   map[string]*ToolOverrideConfig   `json:"tools,omitempty"`
	Master  *toolOverrideFragment            `json:"master,omitempty"`
	Servers map[string]*toolOverrideFragment `json:"servers,omitempty"`
}

type toolOverrideFragment struct {
	Enabled *bool                          `json:"enabled,omitempty"`
	Tools   map[string]*ToolOverrideConfig `json:"tools,omitempty"`
}

type ToolOverrideSet struct {
	ToolOverrides map[string]*ToolOverrideConfig
	Master        *toolOverrideFragment
	Servers       map[string]*toolOverrideFragment
}

func loadToolOverridesFromPath(path string) (*ToolOverrideSet, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	normalized, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve override path: %w", err)
	}
	data, err := os.ReadFile(normalized)
	if err != nil {
		return nil, err
	}
	var raw toolOverrideFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse override file %s: %w", normalized, err)
	}
	set := &ToolOverrideSet{
		ToolOverrides: make(map[string]*ToolOverrideConfig),
		Servers:       make(map[string]*toolOverrideFragment),
	}
	mergeToolOverrideInto(set.ToolOverrides, raw.Tools)
	for name, fragment := range raw.Servers {
		if fragment == nil {
			continue
		}
		copyFragment := &toolOverrideFragment{}
		if fragment.Enabled != nil {
			copyFragment.Enabled = copyBoolPointer(fragment.Enabled)
		}
		if len(fragment.Tools) > 0 {
			copyFragment.Tools = make(map[string]*ToolOverrideConfig, len(fragment.Tools))
			for toolName, cfg := range fragment.Tools {
				copyFragment.Tools[toolName] = copyToolOverrideConfig(cfg)
			}
			mergeToolOverrideInto(set.ToolOverrides, fragment.Tools)
		}
		set.Servers[name] = copyFragment
	}
	if raw.Master != nil {
		set.Master = &toolOverrideFragment{}
		if raw.Master.Enabled != nil {
			set.Master.Enabled = copyBoolPointer(raw.Master.Enabled)
		}
		if len(raw.Master.Tools) > 0 {
			set.Master.Tools = make(map[string]*ToolOverrideConfig, len(raw.Master.Tools))
			for toolName, cfg := range raw.Master.Tools {
				set.Master.Tools[toolName] = copyToolOverrideConfig(cfg)
			}
			mergeToolOverrideInto(set.ToolOverrides, raw.Master.Tools)
		}
	}
	if len(set.ToolOverrides) == 0 && set.Master == nil && len(set.Servers) == 0 {
		return nil, nil
	}
	return set, nil
}

func mergeToolOverrideInto(dest map[string]*ToolOverrideConfig, src map[string]*ToolOverrideConfig) {
	if len(src) == 0 {
		return
	}
	if dest == nil {
		return
	}
	for name, cfg := range src {
		if cfg == nil {
			continue
		}
		copyCfg := copyToolOverrideConfig(cfg)
		if existing, ok := dest[name]; ok && existing != nil {
			dest[name] = mergeOverrideConfig(existing, copyCfg)
		} else {
			dest[name] = copyCfg
		}
	}
}

func mergeToolOverrideMaps(base, extra map[string]*ToolOverrideConfig) map[string]*ToolOverrideConfig {
	if len(extra) == 0 {
		if base == nil {
			return nil
		}
		return copyToolOverrideMap(base)
	}
	result := copyToolOverrideMap(base)
	if result == nil {
		result = make(map[string]*ToolOverrideConfig)
	}
	mergeToolOverrideInto(result, extra)
	return result
}

func copyToolOverrideMap(in map[string]*ToolOverrideConfig) map[string]*ToolOverrideConfig {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*ToolOverrideConfig, len(in))
	for k, v := range in {
		out[k] = copyToolOverrideConfig(v)
	}
	return out
}

func copyToolOverrideConfig(in *ToolOverrideConfig) *ToolOverrideConfig {
	if in == nil {
		return nil
	}
	out := &ToolOverrideConfig{}
	if in.Annotations != nil {
		out.Annotations = &AnnotationOverrideConfig{}
		out.Annotations.ReadOnlyHint = copyBoolPointer(in.Annotations.ReadOnlyHint)
		out.Annotations.DestructiveHint = copyBoolPointer(in.Annotations.DestructiveHint)
		out.Annotations.IdempotentHint = copyBoolPointer(in.Annotations.IdempotentHint)
		out.Annotations.OpenWorldHint = copyBoolPointer(in.Annotations.OpenWorldHint)
	}
	if in.Enabled != nil {
		out.Enabled = copyBoolPointer(in.Enabled)
	}
	return out
}

func mergeOverrideConfig(base, extra *ToolOverrideConfig) *ToolOverrideConfig {
	if base == nil {
		return copyToolOverrideConfig(extra)
	}
	result := copyToolOverrideConfig(base)
	if extra == nil {
		return result
	}
	if extra.Annotations != nil {
		if result.Annotations == nil {
			result.Annotations = &AnnotationOverrideConfig{}
		}
		if extra.Annotations.ReadOnlyHint != nil {
			result.Annotations.ReadOnlyHint = copyBoolPointer(extra.Annotations.ReadOnlyHint)
		}
		if extra.Annotations.DestructiveHint != nil {
			result.Annotations.DestructiveHint = copyBoolPointer(extra.Annotations.DestructiveHint)
		}
		if extra.Annotations.IdempotentHint != nil {
			result.Annotations.IdempotentHint = copyBoolPointer(extra.Annotations.IdempotentHint)
		}
		if extra.Annotations.OpenWorldHint != nil {
			result.Annotations.OpenWorldHint = copyBoolPointer(extra.Annotations.OpenWorldHint)
		}
	}
	if extra.Enabled != nil {
		result.Enabled = copyBoolPointer(extra.Enabled)
	}
	return result
}

func copyBoolPointer(in *bool) *bool {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func fragmentToolEnabled(fragment *toolOverrideFragment, toolName string) *bool {
	if fragment == nil {
		return nil
	}
	if fragment.Tools != nil {
		if cfg, ok := fragment.Tools[toolName]; ok && cfg != nil && cfg.Enabled != nil {
			return cfg.Enabled
		}
		if cfg, ok := fragment.Tools["*"]; ok && cfg != nil && cfg.Enabled != nil {
			return cfg.Enabled
		}
	}
	return nil
}

func resolveEnabledFlag(overrides *ToolOverrideConfig) *bool {
	if overrides != nil {
		return overrides.Enabled
	}
	return nil
}

func toolEnabled(set *ToolOverrideSet, serverName, toolName string) bool {
	if set == nil {
		return true
	}
	enabled := true
	if set.Master != nil && set.Master.Enabled != nil {
		enabled = *set.Master.Enabled
	}
	if flag := fragmentToolEnabled(set.Master, toolName); flag != nil {
		enabled = *flag
	}
	if fragment := set.Servers[serverName]; fragment != nil {
		if fragment.Enabled != nil {
			enabled = *fragment.Enabled
		}
		if flag := fragmentToolEnabled(fragment, toolName); flag != nil {
			enabled = *flag
		}
	}
	if cfg, ok := set.ToolOverrides["*"]; ok && cfg != nil && cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	if cfg, ok := set.ToolOverrides[toolName]; ok && cfg != nil && cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	return enabled
}

func copyFragment(src *toolOverrideFragment) *toolOverrideFragment {
	if src == nil {
		return nil
	}
	dst := &toolOverrideFragment{}
	if src.Enabled != nil {
		dst.Enabled = copyBoolPointer(src.Enabled)
	}
	if len(src.Tools) > 0 {
		dst.Tools = copyToolOverrideMap(src.Tools)
	}
	return dst
}

func mergeOverrideSets(base, extra *ToolOverrideSet) *ToolOverrideSet {
	if base == nil {
		if extra == nil {
			return nil
		}
		return cloneOverrideSet(extra)
	}
	if extra == nil {
		return base
	}
	result := cloneOverrideSet(base)
	mergeToolOverrideInto(result.ToolOverrides, extra.ToolOverrides)
	for name, fragment := range extra.Servers {
		if fragment == nil {
			continue
		}
		dst := result.Servers[name]
		if dst == nil {
			dst = &toolOverrideFragment{}
			result.Servers[name] = dst
		}
		if fragment.Enabled != nil {
			dst.Enabled = copyBoolPointer(fragment.Enabled)
		}
		if len(fragment.Tools) > 0 {
			if dst.Tools == nil {
				dst.Tools = make(map[string]*ToolOverrideConfig)
			}
			mergeToolOverrideInto(dst.Tools, fragment.Tools)
		}
	}
	if extra.Master != nil {
		if result.Master == nil {
			result.Master = copyFragment(extra.Master)
		} else {
			if extra.Master.Enabled != nil {
				result.Master.Enabled = copyBoolPointer(extra.Master.Enabled)
			}
			if len(extra.Master.Tools) > 0 {
				if result.Master.Tools == nil {
					result.Master.Tools = make(map[string]*ToolOverrideConfig)
				}
				mergeToolOverrideInto(result.Master.Tools, extra.Master.Tools)
			}
		}
	}
	return result
}

func cloneOverrideSet(src *ToolOverrideSet) *ToolOverrideSet {
	if src == nil {
		return nil
	}
	clone := &ToolOverrideSet{
		ToolOverrides: copyToolOverrideMap(src.ToolOverrides),
		Servers:       make(map[string]*toolOverrideFragment, len(src.Servers)),
	}
	if src.Master != nil {
		clone.Master = copyFragment(src.Master)
	}
	for name, fragment := range src.Servers {
		clone.Servers[name] = copyFragment(fragment)
	}
	return clone
}
