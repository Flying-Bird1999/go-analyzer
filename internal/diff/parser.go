package diff

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeaderRE = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func ParseUnified(input []byte) ([]FileChange, error) {
	var changes []FileChange
	var current *FileChange
	newLine := 0

	scanner := bufio.NewScanner(bytes.NewReader(input))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			if current != nil {
				changes = append(changes, *current)
			}
			oldPath, newPath := parseDiffPaths(line)
			current = &FileChange{OldPath: oldPath, NewPath: newPath, Status: StatusModified}
			newLine = 0
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "new file mode"):
			current.Status = StatusAdded
		case strings.HasPrefix(line, "deleted file mode"):
			current.Status = StatusDeleted
		case strings.HasPrefix(line, "--- "):
			current.OldPath = normalizeDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
		case strings.HasPrefix(line, "+++ "):
			current.NewPath = normalizeDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		case strings.HasPrefix(line, "@@ "):
			start, err := parseHunkStart(line)
			if err != nil {
				return nil, err
			}
			newLine = start
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++ "):
			if current.Status != StatusDeleted {
				addLineRange(current, newLine)
			}
			newLine++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- "):
		default:
			if newLine > 0 {
				newLine++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		changes = append(changes, *current)
	}
	return changes, nil
}

func parseDiffPaths(line string) (string, string) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return "", ""
	}
	return normalizeDiffPath(fields[2]), normalizeDiffPath(fields[3])
}

func normalizeDiffPath(path string) string {
	switch {
	case path == "/dev/null":
		return ""
	case strings.HasPrefix(path, "a/"), strings.HasPrefix(path, "b/"):
		return path[2:]
	default:
		return path
	}
}

func parseHunkStart(line string) (int, error) {
	matches := hunkHeaderRE.FindStringSubmatch(line)
	if len(matches) == 0 {
		return 0, fmt.Errorf("invalid hunk header %q", line)
	}
	start, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, err
	}
	return start, nil
}

func addLineRange(change *FileChange, line int) {
	if line <= 0 {
		return
	}
	if len(change.Ranges) == 0 {
		change.Ranges = append(change.Ranges, LineRange{StartLine: line, EndLine: line})
		return
	}
	last := &change.Ranges[len(change.Ranges)-1]
	if line == last.EndLine+1 {
		last.EndLine = line
		return
	}
	change.Ranges = append(change.Ranges, LineRange{StartLine: line, EndLine: line})
}
