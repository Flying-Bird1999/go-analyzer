package gomod

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func ExtractDependencies(data []byte) ([]facts.ModuleDependencyFact, error) {
	deps := map[string]facts.ModuleDependencyFact{}
	replaces := map[string]replaceTarget{}
	inRequireBlock := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "//") {
			continue
		}
		switch {
		case raw == "require (":
			inRequireBlock = true
			continue
		case inRequireBlock && raw == ")":
			inRequireBlock = false
			continue
		case inRequireBlock:
			if dep, ok := parseRequireLine(raw); ok {
				deps[dep.Path] = dep
			}
			continue
		case strings.HasPrefix(raw, "require "):
			if dep, ok := parseRequireLine(strings.TrimSpace(strings.TrimPrefix(raw, "require "))); ok {
				deps[dep.Path] = dep
			}
			continue
		case strings.HasPrefix(raw, "replace "):
			oldPath, target, ok := parseReplaceLine(strings.TrimSpace(strings.TrimPrefix(raw, "replace ")))
			if ok {
				replaces[oldPath] = target
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make([]facts.ModuleDependencyFact, 0, len(deps))
	for path, dep := range deps {
		if target, ok := replaces[path]; ok {
			dep.ReplacePath = target.Path
			dep.ReplaceVersion = target.Version
		}
		dep.ID = moduleDependencyID(dep.Path)
		out = append(out, dep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func DiffModules(oldMod, newMod []byte) ([]facts.ModuleChangeFact, error) {
	oldDeps, err := ExtractDependencies(oldMod)
	if err != nil {
		return nil, err
	}
	newDeps, err := ExtractDependencies(newMod)
	if err != nil {
		return nil, err
	}
	oldByPath := depsByPath(oldDeps)
	newByPath := depsByPath(newDeps)
	paths := map[string]bool{}
	for path := range oldByPath {
		paths[path] = true
	}
	for path := range newByPath {
		paths[path] = true
	}
	var out []facts.ModuleChangeFact
	for path := range paths {
		oldDep, hadOld := oldByPath[path]
		newDep, hasNew := newByPath[path]
		switch {
		case !hadOld && hasNew:
			out = append(out, moduleChange(path, facts.ModuleChangeAdded, oldDep, newDep))
		case hadOld && !hasNew:
			out = append(out, moduleChange(path, facts.ModuleChangeRemoved, oldDep, newDep))
		case hadOld && hasNew && replaceChanged(oldDep, newDep):
			out = append(out, moduleChange(path, facts.ModuleChangeReplaced, oldDep, newDep))
		case hadOld && hasNew && oldDep.Version != newDep.Version:
			if compareVersion(newDep.Version, oldDep.Version) >= 0 {
				out = append(out, moduleChange(path, facts.ModuleChangeUpgraded, oldDep, newDep))
			} else {
				out = append(out, moduleChange(path, facts.ModuleChangeDowngraded, oldDep, newDep))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func depsByPath(deps []facts.ModuleDependencyFact) map[string]facts.ModuleDependencyFact {
	out := map[string]facts.ModuleDependencyFact{}
	for _, dep := range deps {
		out[dep.Path] = dep
	}
	return out
}

func replaceChanged(oldDep, newDep facts.ModuleDependencyFact) bool {
	return oldDep.ReplacePath != newDep.ReplacePath || oldDep.ReplaceVersion != newDep.ReplaceVersion
}

func moduleChange(path string, kind facts.ModuleChangeKind, oldDep, newDep facts.ModuleDependencyFact) facts.ModuleChangeFact {
	return facts.ModuleChangeFact{
		ID:                fmt.Sprintf("module_change:%s:%s", kind, path),
		Path:              path,
		Kind:              kind,
		OldVersion:        oldDep.Version,
		NewVersion:        newDep.Version,
		OldReplacePath:    oldDep.ReplacePath,
		OldReplaceVersion: oldDep.ReplaceVersion,
		NewReplacePath:    newDep.ReplacePath,
		NewReplaceVersion: newDep.ReplaceVersion,
	}
}

func compareVersion(a, b string) int {
	ap := versionParts(a)
	bp := versionParts(b)
	maxLen := len(ap)
	if len(bp) > maxLen {
		maxLen = len(bp)
	}
	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return strings.Compare(a, b)
}

func versionParts(version string) []int {
	version = strings.TrimPrefix(version, "v")
	version = strings.Split(version, "-")[0]
	fields := strings.Split(version, ".")
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

func parseRequireLine(line string) (facts.ModuleDependencyFact, bool) {
	indirect := strings.Contains(line, "// indirect")
	line = stripComment(line)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return facts.ModuleDependencyFact{}, false
	}
	return facts.ModuleDependencyFact{Path: fields[0], Version: fields[1], Indirect: indirect}, true
}

type replaceTarget struct {
	Path    string
	Version string
}

func parseReplaceLine(line string) (string, replaceTarget, bool) {
	parts := strings.Split(line, "=>")
	if len(parts) != 2 {
		return "", replaceTarget{}, false
	}
	oldFields := strings.Fields(stripComment(parts[0]))
	newFields := strings.Fields(stripComment(parts[1]))
	if len(oldFields) == 0 || len(newFields) == 0 {
		return "", replaceTarget{}, false
	}
	target := replaceTarget{Path: newFields[0]}
	if len(newFields) > 1 {
		target.Version = newFields[1]
	}
	return oldFields[0], target, true
}

func stripComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return strings.TrimSpace(line)
}

func moduleDependencyID(path string) string {
	return fmt.Sprintf("module:%s", path)
}
