// extractor.go 实现读取 go.mod 中的依赖与 replace 关系，作为 gomod 三层职责的最底层。
//
// Package gomod 负责把 go.mod 的语义变化桥接到本仓的影响范围分析中，
// 职责分为三层：
//
//  1. extractor.go 的 ExtractDependencies：读取当前 go.mod 的 require / replace，
//     输出依赖事实。
//  2. diff.go 的 DiffModulesFromFileChanges：从 go.mod diff 的新增/删除行恢复
//     module 变更（added/removed/upgraded/downgraded/replaced）。
//  3. usage.go 的 MapModuleUsage：把发生变更的 module 映射到本仓 import usage，
//     精确命中处理函数体直接使用 import alias 的 symbol；只能确认 importing file
//     时降级到 file/declaration；本仓无 import 则标记 unreferenced。
//
// 这样 go.mod 不再作为低置信度的非符号 root 出现，而是经由本仓 usage 进入
// 正常的 symbol/file ChangeFact 传播链路。
package gomod

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// ExtractDependencies 解析 go.mod 内容，返回当前依赖列表。
// 同时支持单行 require 与 require block、单行 replace 与 replace block，
// 并把 replace 目标合并回对应依赖的 ReplacePath/ReplaceVersion 字段。
func ExtractDependencies(data []byte) ([]facts.ModuleDependencyFact, error) {
	deps := map[string]facts.ModuleDependencyFact{}
	replaces := map[string]replaceTarget{}
	// 跟踪当前是否处于 require/replace 块（多行括号形式）内部。
	inRequireBlock := false
	inReplaceBlock := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		// 跳过空行与行内注释（注释整行的情况）。
		if raw == "" || strings.HasPrefix(raw, "//") {
			continue
		}
		switch {
		case raw == "require (":
			inRequireBlock = true
			continue
		case raw == "replace (":
			inReplaceBlock = true
			continue
		case inRequireBlock && raw == ")":
			inRequireBlock = false
			continue
		case inReplaceBlock && raw == ")":
			inReplaceBlock = false
			continue
		case inRequireBlock:
			// require block 内部每行是一条依赖。
			if dep, ok := parseRequireLine(raw); ok {
				deps[dep.Path] = dep
			}
			continue
		case inReplaceBlock:
			// replace block 内部每行是一条替换规则。
			oldPath, target, ok := parseReplaceLine(raw)
			if ok {
				replaces[oldPath] = target
			}
			continue
		case strings.HasPrefix(raw, "require "):
			// 单行 require：去掉关键字后按依赖行解析。
			if dep, ok := parseRequireLine(strings.TrimSpace(strings.TrimPrefix(raw, "require "))); ok {
				deps[dep.Path] = dep
			}
			continue
		case strings.HasPrefix(raw, "replace "):
			// 单行 replace：去掉关键字后按替换行解析。
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
		// 把 replace 信息回填到对应依赖，便于上层判断最终生效版本。
		if target, ok := replaces[path]; ok {
			dep.ReplacePath = target.Path
			dep.ReplaceVersion = target.Version
		}
		dep.ID = moduleDependencyID(dep.Path)
		out = append(out, dep)
	}
	// 按依赖路径排序，保证输出确定性。
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// replaceChanged 判断两个依赖事实的 replace 目标（路径或版本）是否发生变化。
// 用于 diff 阶段识别 replace-only 改动。
func replaceChanged(oldDep, newDep facts.ModuleDependencyFact) bool {
	return oldDep.ReplacePath != newDep.ReplacePath || oldDep.ReplaceVersion != newDep.ReplaceVersion
}

// moduleChange 构造一条 ModuleChangeFact，统一填入变更前后的版本与 replace 信息。
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

// compareVersion 按语义版本规则比较两个 module 版本字符串。
//
// 为什么不直接用字符串排序：Go module 版本可能是 v1.2.3、v1.2.3-rc.1、
// pseudo version（v0.0.0-20240102030405-abcdef）、+incompatible 等，
// 字符串排序会给出错误顺序（例如 "rc.10" < "rc.2"）。这里按 semver 规则：
//   - 拆出 core（major.minor.patch）和 prerelease，忽略 build metadata（"+" 之后）；
//   - core 按数值逐段比较；
//   - 没有 prerelease 的版本高于有 prerelease 的版本；
//   - prerelease 内数值标识符按数值比较、字母标识符按字典序比较、数值小于字母。
//
// 任一侧无法解析为 semver 时退化为字符串比较，保持稳定可预测。
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
		// 正式版高于预发布版。
		return 1
	case right.prerelease == "":
		return -1
	default:
		return comparePrerelease(left.prerelease, right.prerelease)
	}
}

// semanticVersion 是 parseSemanticVersion 的解析结果。
type semanticVersion struct {
	core       [3]string // core[i] 保存 major/minor/patch 的原始数字字符串，便于逐段数值比较
	prerelease string    // prerelease 片段（不含前导 "-"），缺省为空表示正式版
}

// parseSemanticVersion 把 "v1.2.3-rc.1+incompatible" 风格版本拆成 core 与 prerelease。
// build metadata（"+" 之后）直接丢弃：它不影响版本优先级。
// core 不足三段时用 "0" 补齐，便于后续逐段比较长度一致。
func parseSemanticVersion(version string) (semanticVersion, bool) {
	version = strings.TrimPrefix(version, "v")
	// 丢弃 build metadata（+incompatible 等）。
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

// comparePrerelease 按 semver 规则比较两个预发布字符串。
// 预发布按点分段逐一比较：纯数值段按数值比较且小于字母段，字母段按字典序比较；
// 所有并列段相同时，分段数多者视为更大。
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
			// 数值标识符优先级低于字母标识符。
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

// compareNumericIdentifier 比较两个纯数字字符串的数值大小。
// 先去除前导零再比较长度和字典序，避免 "010" 与 "10" 的歧义。
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

// isNumericIdentifier 判断字符串是否只由数字组成（semver 中的数值标识符）。
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

// parseRequireLine 解析 require 的一行内容（已去掉 require 关键字）。
// 形如 "example.com/pkg v1.2.3" 或 "example.com/pkg v1.2.3 // indirect"。
// 同时识别 indirect 标记并去掉行尾注释。
func parseRequireLine(line string) (facts.ModuleDependencyFact, bool) {
	indirect := strings.Contains(line, "// indirect")
	line = stripComment(line)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return facts.ModuleDependencyFact{}, false
	}
	return facts.ModuleDependencyFact{Path: fields[0], Version: fields[1], Indirect: indirect}, true
}

// replaceTarget 是 parseReplaceLine 解析出的替换目标，对应 "=>" 右侧。
type replaceTarget struct {
	Path    string
	Version string // 本地路径替换时可能为空
}

// parseReplaceLine 解析 replace 的一行内容（已去掉 replace 关键字）。
// 形如 "example.com/old => example.com/new v1.2.3" 或 "example.com/old => ../local"。
// 返回原始 path 与替换目标；无法识别时 ok=false。
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

// stripComment 去掉行内的 "//" 注释并 trim 两端空白。
func stripComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return strings.TrimSpace(line)
}

// moduleDependencyID 拼装形如 module:<path> 的稳定依赖 ID。
func moduleDependencyID(path string) string {
	return fmt.Sprintf("module:%s", path)
}
