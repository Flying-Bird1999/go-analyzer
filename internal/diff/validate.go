package diff

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateApplied verifies that the diff describes the current project snapshot.
func ValidateApplied(root string, changes []FileChange) error {
	for _, change := range changes {
		path := change.NewPath
		if change.Status == StatusDeleted {
			path = change.OldPath
		}
		fullPath, err := projectPath(root, path)
		if err != nil {
			return err
		}

		if change.Status == StatusDeleted {
			if _, err := os.Stat(fullPath); err == nil {
				return fmt.Errorf("diff for %q does not match the post-change source: deleted file still exists", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("validate deleted file %q: %w", path, err)
			}
			continue
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("diff for %q does not match the post-change source: %w", path, err)
		}
		lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
		for _, expected := range change.ExpectedLines {
			if expected.Line <= 0 || expected.Line > len(lines) || lines[expected.Line-1] != expected.Text {
				return fmt.Errorf(
					"diff for %q does not match the post-change source at line %d",
					path,
					expected.Line,
				)
			}
		}
	}
	return nil
}

func projectPath(root, path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if path == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe diff path %q", path)
	}
	return filepath.Join(root, clean), nil
}
