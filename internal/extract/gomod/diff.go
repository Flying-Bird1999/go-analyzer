package gomod

import (
	"bufio"
	"sort"
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
		out = append(out, diffModulesFromPatch(change.Raw)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

type patchModule struct {
	dependency facts.ModuleDependencyFact
	require    bool
	replace    bool
}

func diffModulesFromPatch(raw string) []facts.ModuleChangeFact {
	oldModules := map[string]patchModule{}
	newModules := map[string]patchModule{}
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
			addPatchModule(oldModules, strings.TrimPrefix(line, "-"))
		case strings.HasPrefix(line, "+"):
			addPatchModule(newModules, strings.TrimPrefix(line, "+"))
		}
	}

	paths := map[string]bool{}
	for path := range oldModules {
		paths[path] = true
	}
	for path := range newModules {
		paths[path] = true
	}
	var out []facts.ModuleChangeFact
	for path := range paths {
		oldModule, hadOld := oldModules[path]
		newModule, hasNew := newModules[path]
		oldDep := oldModule.dependency
		newDep := newModule.dependency
		switch {
		case !hadOld && hasNew && newModule.require:
			out = append(out, moduleChange(path, facts.ModuleChangeAdded, oldDep, newDep))
		case hadOld && !hasNew && oldModule.require:
			out = append(out, moduleChange(path, facts.ModuleChangeRemoved, oldDep, newDep))
		case oldModule.replace != newModule.replace || replaceChanged(oldDep, newDep):
			out = append(out, moduleChange(path, facts.ModuleChangeReplaced, oldDep, newDep))
		case oldModule.require && newModule.require && oldDep.Version != newDep.Version:
			kind := facts.ModuleChangeDowngraded
			if compareVersion(newDep.Version, oldDep.Version) >= 0 {
				kind = facts.ModuleChangeUpgraded
			}
			out = append(out, moduleChange(path, kind, oldDep, newDep))
		}
	}
	return out
}

func addPatchModule(modules map[string]patchModule, raw string) {
	line := strings.TrimSpace(raw)
	if line == "" || line == ")" || strings.HasPrefix(line, "//") {
		return
	}
	if strings.HasPrefix(line, "require ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
	}
	if dep, ok := parseRequireLine(line); ok && strings.HasPrefix(dep.Version, "v") {
		current := modules[dep.Path]
		current.dependency.Path = dep.Path
		current.dependency.Version = dep.Version
		current.dependency.Indirect = dep.Indirect
		current.require = true
		modules[dep.Path] = current
		return
	}
	if strings.HasPrefix(line, "replace ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "replace "))
	}
	oldPath, target, ok := parseReplaceLine(line)
	if !ok {
		return
	}
	current := modules[oldPath]
	current.dependency.Path = oldPath
	current.dependency.ReplacePath = target.Path
	current.dependency.ReplaceVersion = target.Version
	current.replace = true
	modules[oldPath] = current
}
