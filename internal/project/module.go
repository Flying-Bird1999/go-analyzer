package project

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ReadModulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			modulePath := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			if modulePath != "" {
				return modulePath, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan go.mod: %w", err)
	}
	return "", fmt.Errorf("module path not found in %s", filepath.Join(root, "go.mod"))
}
