// parser.go 实现统一差异（unified diff）的逐行解析，产出按文件组织的变更描述。
//
// Package diff 解析 MR 提交的 unified diff，将每个文件的变更信息结构化：
//   - 记录文件 old/new 路径与 added/deleted/modified 状态。
//   - 用新版本（变更后）行号范围描述新增/上下文行，删除行则单独保存为删除块。
//   - 把连续删除块连同其旧行号、新版本锚点行号和原文一并保留，供 deleted route 恢复使用。
//   - 保存每个文件的原始 patch 文本与变更后期望出现的行内容，供后续校验。
//
// 解析完成后，mapper 按领域事实优先级（注解 -> route group -> route -> 中间件 ->
// 符号 -> 文件）把 diff 行范围映射到最精确的语义根；validate 则在构建 AST 前逐行
// 校验 diff 已应用到变更后源码，确保行号与 AST 严格匹配。
package diff

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// hunkHeaderRE 匹配 unified diff 的 hunk 头部，例如 `@@ -10,6 +10,8 @@`，
// 仅提取旧行起点和新行起点两个数字，长度部分可选。
var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// ParseUnified 解析整份 unified diff 文本，返回按文件组织的变更列表。
//
// 该解析器维护一个面向 hunk 的状态机，跟踪当前文件的旧/新行号计数器，识别
// `diff --git`、状态行、`---`/`+++` 路径行、`@@` hunk 头以及 `+`/`-`/` ` 行，
// 同时把连续删除行聚合成删除块。任何无法识别 hunk 头的输入都会返回错误；
// 完全不包含文件变更的空 diff 也返回错误。
func ParseUnified(input []byte) ([]FileChange, error) {
	// changes 为最终返回的文件变更列表。
	var changes []FileChange
	// current 指向正在解析的文件，遇到下一个 `diff --git` 时刷出并置空。
	var current *FileChange
	// rawLines 累积当前文件的原始 diff 行，用于重建 Raw patch 文本。
	var rawLines []string
	// oldLine/newLine 是当前 hunk 内的旧/新版本行号计数器，按行前进。
	oldLine := 0
	newLine := 0
	// hunkActive 标记是否处于某个 `@@ ... @@` hunk 内部。
	hunkActive := false
	// deletionPending 标记是否正处于连续删除行的累积过程中。
	deletionPending := false
	// deletionAnchor 记录删除块对应的新版本锚点行号（紧随删除块的下一行），
	// 用于在 mapper 阶段把删除行映射到 surviving 内容。
	deletionAnchor := 0
	// deletedOldStart/deletedNewAnchor 记录当前删除块的旧行起点与新版本锚点。
	deletedOldStart := 0
	deletedNewAnchor := 0
	// deletedLines 缓存当前连续删除块去掉前导 `-` 后的原文。
	var deletedLines []string

	// flushDeletion 结束当前正在累积的删除块。
	// addAnchor 为 true 时（遇到 context/added 行或 hunk 结束）追加一个
	// RangeKindDeletionAnchor 行范围，让 mapper 据此生成 medium confidence root；
	// 为 false 时（遇到 added 行）表示删除已被新增内容替换，仅保存删除块本身。
	flushDeletion := func(addAnchor bool) {
		if current == nil || !deletionPending {
			return
		}
		// 锚点行号至少为 1，避免删除出现在文件首行时落到第 0 行。
		anchor := deletedNewAnchor
		if anchor <= 0 {
			anchor = 1
		}
		if len(deletedLines) > 0 {
			// 复制一份删除行，避免后续被重置时影响已保存的切片。
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
		// 重置删除累积状态，准备处理下一个删除块。
		deletionPending = false
		deletionAnchor = 0
		deletedOldStart = 0
		deletedNewAnchor = 0
		deletedLines = nil
	}
	// flushHunk 在 hunk 结束（遇到新 hunk 或新文件）时刷出尚未闭合的删除块。
	flushHunk := func() {
		if current == nil || !hunkActive {
			return
		}
		flushDeletion(true)
		hunkActive = false
	}
	// flushCurrent 在遇到下一个文件或解析结束时刷出当前文件：
	// 先关闭 hunk 与删除块，再把累积的原始行拼成 Raw 文本，加入返回列表。
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

	// 用大缓冲扫描，避免超长行或超大 diff 触发 bufio 默认 64KiB 上限。
	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			// 进入新文件：先刷出上一个文件，再用 diff --git 行解析出的路径初始化。
			flushCurrent()
			oldPath, newPath := parseDiffPaths(line)
			current = &FileChange{OldPath: oldPath, NewPath: newPath, Status: StatusModified}
			rawLines = []string{line}
			oldLine = 0
			newLine = 0
			continue
		}
		if current == nil {
			// 还没遇到任何 `diff --git` 头，跳过可能存在的前导噪声行。
			continue
		}
		rawLines = append(rawLines, line)
		switch {
		case strings.HasPrefix(line, "new file mode"):
			current.Status = StatusAdded
		case strings.HasPrefix(line, "deleted file mode"):
			current.Status = StatusDeleted
		case strings.HasPrefix(line, "GIT binary patch"), strings.HasPrefix(line, "Binary files "):
			// binary patch：关闭 hunk 解析，后续 base85 行不应被当作内容行。
			hunkActive = false
		case strings.HasPrefix(line, "@@ "):
			// 新 hunk 开始：先关闭上一个 hunk 的删除块，再解析行号起点。
			flushHunk()
			oldStart, newStart, err := parseHunkStart(line)
			if err != nil {
				return nil, err
			}
			oldLine = oldStart
			newLine = newStart
			hunkActive = true
		case strings.HasPrefix(line, "@@"):
			// 以 @@ 开头但不是合法的 unified `@@ ` 头（该情况已在上一 case 消费）：
			// 组合 diff 的 `@@@ ... @@@`、
			// 或畸形/截断的 hunk 头。静默跳过会让该文件产出"零变更"的假结论（漏报），
			// 故直接报错，与本解析器只接受标准 unified diff 的契约一致。
			return nil, fmt.Errorf("unsupported diff hunk header (combined diffs are not supported): %q", line)
		case !hunkActive && strings.HasPrefix(line, "--- "):
			// `---`/`+++` 行只在非 hunk 区识别为路径头，避免 hunk 内以 `-- ` 开头的
			// 删除行（如 SQL 注释）被误当成路径头。
			current.OldPath = normalizeDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
		case !hunkActive && strings.HasPrefix(line, "+++ "):
			current.NewPath = normalizeDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		case hunkActive && strings.HasPrefix(line, "+"):
			// 新增行：删除被新增替换，故只刷出删除块但不追加 anchor range。
			flushDeletion(false)
			if current.Status != StatusDeleted {
				addLineRange(current, newLine, RangeKindAdded)
				// ExpectedLines 记录变更后源码该行应出现的文本，供 validate 校验。
				current.ExpectedLines = append(current.ExpectedLines, ExpectedLine{
					Line: newLine,
					Text: strings.TrimPrefix(line, "+"),
				})
			}
			newLine++
		case hunkActive && strings.HasPrefix(line, "-"):
			// 删除行：进入或延续删除块累积，记下新版本锚点（紧随其后那行）。
			if !deletionPending {
				deletionPending = true
				deletionAnchor = newLine
				deletedOldStart = oldLine
				deletedNewAnchor = newLine
			}
			deletedLines = append(deletedLines, strings.TrimPrefix(line, "-"))
			oldLine++
		case hunkActive && strings.HasPrefix(line, " "):
			// 上下文行：删除块到此结束，追加 anchor range 以便 mapper 命中 surviving 内容。
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
		// 空 diff 视为非法输入，避免后续阶段在无 ChangeFact 的情况下静默继续。
		return nil, fmt.Errorf("unified diff contains no file changes")
	}
	return changes, nil
}

// parseDiffPaths 从 `diff --git a/x b/y` 行中拆出归一化后的 old/new 路径。
// 路径可能带引号（含特殊字符），由 nextDiffPathToken 统一处理。
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

// nextDiffPathToken 从 input 起始处读取一个 diff 路径 token，按空白分隔。
// 当 token 以双引号开头时按带引号字符串解析（处理转义），返回 token、剩余串和是否成功。
func nextDiffPathToken(input string) (string, string, bool) {
	input = strings.TrimLeft(input, " \t")
	if input == "" {
		return "", "", false
	}
	if input[0] != '"' {
		// 普通路径：遇到第一个空白即结束。
		for i, r := range input {
			if r == ' ' || r == '\t' {
				return input[:i], input[i:], true
			}
		}
		return input, "", true
	}
	// 带引号路径：处理 `\"` 转义，找到未转义的闭合引号为止。
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

// normalizeDiffPath 把 diff 中的路径归一化为项目相对路径：
// `/dev/null` 表示新增/删除端缺失，归一化为空串；`a/`/`b/` 前缀被剥离。
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

// unquoteDiffPath 对带双引号（git core.quotepath 风格 C 风格转义）的路径做反转义。
// 非引号包裹的路径原样返回；反转义失败也原样返回，交给上层判断。
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

// parseHunkStart 解析 `@@ -oldStart,count +newStart,count @@` 形式的 hunk 头，
// 返回旧版本和新版本的起始行号。无法匹配时返回错误。
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

// addLineRange 向 change 追加一个单行范围，并与上一个范围按 kind 合并：
// 仅当新行恰好紧接上一个范围末行且 kind 相同时延长上一个范围，否则新建独立范围。
// 这种合并方式让 mapper 收到的是连续区段而非逐行碎片。
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
