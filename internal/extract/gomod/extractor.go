package gomod

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
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
	left, leftOK := parseSemanticVersion(a)
	right, rightOK := parseSemanticVersion(b)
	if !leftOK || !rightOK {
		return strings.Compare(a, b)
	}
	for i := range left.core {
		if comparison := compareNumericIdentifier(left.core[i], right.core[i]); comparison != 0 {
			return comparison
		}
	}
	switch {
	case left.prerelease == "" && right.prerelease == "":
		return 0
	case left.prerelease == "":
		return 1
	case right.prerelease == "":
		return -1
	default:
		return comparePrerelease(left.prerelease, right.prerelease)
	}
}

type semanticVersion struct {
	core       [3]string
	prerelease string
}

func parseSemanticVersion(version string) (semanticVersion, bool) {
	version = strings.TrimPrefix(version, "v")
	if index := strings.IndexByte(version, '+'); index >= 0 {
		version = version[:index]
	}
	prerelease := ""
	if index := strings.IndexByte(version, '-'); index >= 0 {
		prerelease = version[index+1:]
		version = version[:index]
	}
	fields := strings.Split(version, ".")
	if len(fields) == 0 || len(fields) > 3 {
		return semanticVersion{}, false
	}
	var parsed semanticVersion
	parsed.prerelease = prerelease
	for i := range parsed.core {
		parsed.core[i] = "0"
	}
	for i, field := range fields {
		if !isNumericIdentifier(field) {
			return semanticVersion{}, false
		}
		parsed.core[i] = field
	}
	return parsed, true
}

func comparePrerelease(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for i := 0; i < len(leftParts) && i < len(rightParts); i++ {
		leftNumeric := isNumericIdentifier(leftParts[i])
		rightNumeric := isNumericIdentifier(rightParts[i])
		switch {
		case leftNumeric && rightNumeric:
			if comparison := compareNumericIdentifier(leftParts[i], rightParts[i]); comparison != 0 {
				return comparison
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		default:
			if comparison := strings.Compare(leftParts[i], rightParts[i]); comparison != 0 {
				return comparison
			}
		}
	}
	return len(leftParts) - len(rightParts)
}

func compareNumericIdentifier(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	if len(left) != len(right) {
		return len(left) - len(right)
	}
	return strings.Compare(left, right)
}

func isNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
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
