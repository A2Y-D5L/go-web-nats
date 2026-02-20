package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func writeFileIfMissing(path string, data []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirModePrivateRead); err != nil {
		return false, err
	}
	writeErr := os.WriteFile(path, data, fileModePrivate)
	if writeErr != nil {
		return false, writeErr
	}
	return true, nil
}

func upsertFile(path string, data []byte) (bool, error) {
	prev, err := os.ReadFile(path)
	if err == nil && string(prev) == string(data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	mkdirErr := os.MkdirAll(filepath.Dir(path), dirModePrivateRead)
	if mkdirErr != nil {
		return false, mkdirErr
	}
	writeErr := os.WriteFile(path, data, fileModePrivate)
	if writeErr != nil {
		return false, writeErr
	}
	return true, nil
}

func relPath(baseDir, fullPath string) string {
	rel, err := filepath.Rel(baseDir, fullPath)
	if err != nil {
		return filepath.ToSlash(fullPath)
	}
	return filepath.ToSlash(rel)
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		set[filepath.ToSlash(v)] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
