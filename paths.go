package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func configHome() string {
	if v := strings.TrimSpace(os.Getenv("STELAE_CONFIG_HOME")); v != "" {
		return filepath.Clean(v)
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "stelae")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "stelae")
}

func stateHome() string {
	if v := strings.TrimSpace(os.Getenv("STELAE_STATE_HOME")); v != "" {
		return filepath.Clean(v)
	}
	return filepath.Join(configHome(), ".state")
}

func requireHomePath(home, target string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("empty home path")
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absHome, absTarget)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", errors.New("path escapes configured home")
	}
	return absTarget, nil
}

func mkdirAllUnder(home, target string) (string, error) {
	path, err := requireHomePath(home, target)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func envEnabled(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func resolveGuardedPath(target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return target, nil
	}
	if resolved, err := requireHomePath(configHome(), target); err == nil {
		return resolved, nil
	}
	if resolved, err := requireHomePath(stateHome(), target); err == nil {
		return resolved, nil
	}
	return "", errors.New("path must be under config or state home")
}
