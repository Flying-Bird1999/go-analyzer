package gomod

import (
	"bufio"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func DiffModulesFromFileChanges(fileChanges []diff.FileChange) ([]facts.ModuleChangeFact, error) {
	var out []facts.ModuleChangeFact
	for _, change := range fileChanges {
		file := change.NewPath
		if file == "" {
			file = change.OldPath
		}
		if file != "go.mod" || change.Raw == "" {
			continue
		}
		oldMod, newMod := goModSnapshotsFromPatch(change.Raw)
		changes, err := DiffModules([]byte(oldMod), []byte(newMod))
		if err != nil {
			return nil, err
		}
		out = append(out, changes...)
	}
	return out, nil
}

func goModSnapshotsFromPatch(raw string) (string, string) {
	var oldLines []string
	var newLines []string
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "),
			strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "@@ "),
			strings.HasPrefix(line, "--- "),
			strings.HasPrefix(line, "+++ "):
			continue
		case strings.HasPrefix(line, "-"):
			oldLines = append(oldLines, strings.TrimPrefix(line, "-"))
		case strings.HasPrefix(line, "+"):
			newLines = append(newLines, strings.TrimPrefix(line, "+"))
		case strings.HasPrefix(line, " "):
			text := strings.TrimPrefix(line, " ")
			oldLines = append(oldLines, text)
			newLines = append(newLines, text)
		}
	}
	return strings.Join(oldLines, "\n") + "\n", strings.Join(newLines, "\n") + "\n"
}
