package diff

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func ParseUnified(input []byte) ([]FileChange, error) {
	var changes []FileChange
	var current *FileChange
	var rawLines []string
	oldLine := 0
	newLine := 0
	hunkActive := false
	deletionPending := false
	deletionAnchor := 0
	deletedOldStart := 0
	deletedNewAnchor := 0
	var deletedLines []string

	flushDeletion := func(addAnchor bool) {
		if current == nil || !deletionPending {
			return
		}
		anchor := deletedNewAnchor
		if anchor <= 0 {
			anchor = 1
		}
		if len(deletedLines) > 0 {
			lines := append([]string(nil), deletedLines...)
			current.DeletedBlocks = append(current.DeletedBlocks, DeletedBlock{
				OldStartLine:  deletedOldStart,
				NewAnchorLine: anchor,
				Lines:         lines,
			})
		}
		if addAnchor {
			line := deletionAnchor
			if line <= 0 {
				line = 1
			}
			addLineRange(current, line, RangeKindDeletionAnchor)
		}
		deletionPending = false
		deletionAnchor = 0
		deletedOldStart = 0
		deletedNewAnchor = 0
		deletedLines = nil
	}
	flushHunk := func() {
		if current == nil || !hunkActive {
			return
		}
		flushDeletion(true)
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
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			flushCurrent()
			oldPath, newPath := parseDiffPaths(line)
			current = &FileChange{OldPath: oldPath, NewPath: newPath, Status: StatusModified}
			rawLines = []string{line}
			oldLine = 0
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
			oldStart, newStart, err := parseHunkStart(line)
			if err != nil {
				return nil, err
			}
			oldLine = oldStart
			newLine = newStart
			hunkActive = true
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++ "):
			flushDeletion(false)
			if current.Status != StatusDeleted {
				addLineRange(current, newLine, RangeKindAdded)
				current.ExpectedLines = append(current.ExpectedLines, ExpectedLine{
					Line: newLine,
					Text: strings.TrimPrefix(line, "+"),
				})
			}
			newLine++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- "):
			if !deletionPending {
				deletionPending = true
				deletionAnchor = newLine
				deletedOldStart = oldLine
				deletedNewAnchor = newLine
			}
			deletedLines = append(deletedLines, strings.TrimPrefix(line, "-"))
			oldLine++
		case strings.HasPrefix(line, " "):
			flushDeletion(true)
			if current.Status != StatusDeleted {
				current.ExpectedLines = append(current.ExpectedLines, ExpectedLine{
					Line: newLine,
					Text: strings.TrimPrefix(line, " "),
				})
			}
			oldLine++
			newLine++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flushCurrent()
	if len(changes) == 0 {
		return nil, fmt.Errorf("unified diff contains no file changes")
	}
	return changes, nil
}

func parseDiffPaths(line string) (string, string) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	oldPath, rest, ok := nextDiffPathToken(rest)
	if !ok {
		return "", ""
	}
	newPath, _, ok := nextDiffPathToken(rest)
	if !ok {
		return "", ""
	}
	return normalizeDiffPath(oldPath), normalizeDiffPath(newPath)
}

func nextDiffPathToken(input string) (string, string, bool) {
	input = strings.TrimLeft(input, " \t")
	if input == "" {
		return "", "", false
	}
	if input[0] != '"' {
		for i, r := range input {
			if r == ' ' || r == '\t' {
				return input[:i], input[i:], true
			}
		}
		return input, "", true
	}
	escaped := false
	for i := 1; i < len(input); i++ {
		switch {
		case escaped:
			escaped = false
		case input[i] == '\\':
			escaped = true
		case input[i] == '"':
			return input[:i+1], input[i+1:], true
		}
	}
	return "", "", false
}

func normalizeDiffPath(path string) string {
	path = unquoteDiffPath(path)
	switch {
	case path == "/dev/null":
		return ""
	case strings.HasPrefix(path, "a/"), strings.HasPrefix(path, "b/"):
		return path[2:]
	default:
		return path
	}
}

func unquoteDiffPath(path string) string {
	if len(path) < 2 || path[0] != '"' || path[len(path)-1] != '"' {
		return path
	}
	unquoted, err := strconv.Unquote(path)
	if err != nil {
		return path
	}
	return unquoted
}

func parseHunkStart(line string) (int, int, error) {
	matches := hunkHeaderRE.FindStringSubmatch(line)
	if len(matches) == 0 {
		return 0, 0, fmt.Errorf("invalid hunk header %q", line)
	}
	oldStart, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, err
	}
	newStart, err := strconv.Atoi(matches[2])
	if err != nil {
		return 0, 0, err
	}
	return oldStart, newStart, nil
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
