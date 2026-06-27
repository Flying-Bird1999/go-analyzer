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
	var rawLines []string
	newLine := 0
	hunkActive := false
	deletionPending := false
	deletionAnchor := 0

	flushDeletion := func() {
		if current == nil || !deletionPending {
			return
		}
		anchor := deletionAnchor
		if anchor <= 0 {
			anchor = 1
		}
		addLineRange(current, anchor, RangeKindDeletionAnchor)
		deletionPending = false
		deletionAnchor = 0
	}
	flushHunk := func() {
		if current == nil || !hunkActive {
			return
		}
		flushDeletion()
		hunkActive = false
	}
	flushCurrent := func() {
		if current == nil {
			return
		}
		flushHunk()
		if len(rawLines) > 0 {
			current.Raw = strings.Join(rawLines, "\n") + "\n"
		}
		changes = append(changes, *current)
	}

	scanner := bufio.NewScanner(bytes.NewReader(input))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			flushCurrent()
			oldPath, newPath := parseDiffPaths(line)
			current = &FileChange{OldPath: oldPath, NewPath: newPath, Status: StatusModified}
			rawLines = []string{line}
			newLine = 0
			continue
		}
		if current == nil {
			continue
		}
		rawLines = append(rawLines, line)
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
			flushHunk()
			start, err := parseHunkStart(line)
			if err != nil {
				return nil, err
			}
			newLine = start
			hunkActive = true
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++ "):
			deletionPending = false
			deletionAnchor = 0
			if current.Status != StatusDeleted {
				addLineRange(current, newLine, RangeKindAdded)
			}
			newLine++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- "):
			if !deletionPending {
				deletionPending = true
				deletionAnchor = newLine
			}
		case strings.HasPrefix(line, " "):
			flushDeletion()
			newLine++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flushCurrent()
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

func addLineRange(change *FileChange, line int, kind RangeKind) {
	if line <= 0 {
		return
	}
	if len(change.Ranges) == 0 {
		change.Ranges = append(change.Ranges, LineRange{StartLine: line, EndLine: line, Kind: kind})
		return
	}
	last := &change.Ranges[len(change.Ranges)-1]
	if line == last.EndLine+1 && last.Kind == kind {
		last.EndLine = line
		return
	}
	change.Ranges = append(change.Ranges, LineRange{StartLine: line, EndLine: line, Kind: kind})
}
