// diff.go 实现从 go.mod 的 unified diff 恢复 module 变更（gomod 三层职责中的第二层）。
package gomod

import (
	"bufio"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// DiffModulesFromFileChanges 遍历 diff 中的文件变更，只关心 go.mod，
// 把它的 patch 解析成 ModuleChangeFact 列表。
// 输出按 (path, kind) 排序，保证跨运行稳定。
func DiffModulesFromFileChanges(fileChanges []diff.FileChange) ([]facts.ModuleChangeFact, error) {
	var out []facts.ModuleChangeFact
	for _, change := range fileChanges {
		// 删除文件时 NewPath 为空，回退到 OldPath 判断是否是 go.mod。
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

// patchModule 在 diff 解析过程中记录某个 module 在旧/新版本各自的依赖信息，
// require/replace 标记用于后续判断变更类型。
type patchModule struct {
	dependency facts.ModuleDependencyFact
	require    bool // 该 module 在此侧以 require 形式出现
	replace    bool // 该 module 在此侧以 replace 形式出现
}

// diffModulesFromPatch 解析单个 go.mod patch 文本，恢复 module 变更。
//
// 关键点：不依赖 hunk header 中是否包含 "require ("，而是按行前缀 "+"/"-"
// 分别收集新/旧版本两侧的 require/replace 行。这样即使 hunk 只覆盖 require
// block 内部的单行（context 行不含 "require ("）也能正确识别。
func diffModulesFromPatch(raw string) []facts.ModuleChangeFact {
	oldModules := map[string]patchModule{}
	newModules := map[string]patchModule{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		// 跳过 diff 元信息行：file header / hunk header / index 等。
		case strings.HasPrefix(line, "diff --git "),
			strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "@@ "),
			strings.HasPrefix(line, "--- "),
			strings.HasPrefix(line, "+++ "):
			continue
		case strings.HasPrefix(line, "-"):
			// 旧版本侧的行。
			addPatchModule(oldModules, strings.TrimPrefix(line, "-"))
		case strings.HasPrefix(line, "+"):
			// 新版本侧的行。
			addPatchModule(newModules, strings.TrimPrefix(line, "+"))
		}
	}

	// 合并两侧出现的所有 module path，逐一比较判断变更类型。
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
			// 旧侧没有、新侧是新增的 require -> added。
			out = append(out, moduleChange(path, facts.ModuleChangeAdded, oldDep, newDep))
		case hadOld && !hasNew && oldModule.require:
			// 旧侧有、新侧删除了 require -> removed。
			out = append(out, moduleChange(path, facts.ModuleChangeRemoved, oldDep, newDep))
		case oldModule.replace != newModule.replace || replaceChanged(oldDep, newDep):
			// replace 出现/消失，或替换目标发生变化 -> replaced。
			out = append(out, moduleChange(path, facts.ModuleChangeReplaced, oldDep, newDep))
		case oldModule.require && newModule.require && oldDep.Version != newDep.Version:
			// require 版本变化：按语义版本判断升级还是降级。
			kind := facts.ModuleChangeDowngraded
			if compareVersion(newDep.Version, oldDep.Version) >= 0 {
				kind = facts.ModuleChangeUpgraded
			}
			out = append(out, moduleChange(path, kind, oldDep, newDep))
		}
	}
	return out
}

// addPatchModule 把 diff 行（已去掉 +/- 前缀）解析后写入对应侧的 module 表。
// 同时处理 require/replace 关键字前缀、单行与 block 内部行格式，以及 block 结束括号。
func addPatchModule(modules map[string]patchModule, raw string) {
	line := strings.TrimSpace(raw)
	// 跳过空行、block 结束括号和整行注释。
	if line == "" || line == ")" || strings.HasPrefix(line, "//") {
		return
	}
	// 去掉行首的 "require "/"replace " 关键字，使 block 内行与单行格式统一。
	if strings.HasPrefix(line, "require ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
	}
	// 先按 require 尝试解析；只有 version 以 "v" 开头才视为合法 require 行，
	// 避免把 replace 中的本地路径误判为依赖版本。
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
