// validate.go 校验 diff 是否已经应用到变更后的项目源码：逐行核对每条 ExpectedLine
// 与磁盘内容是否一致，并对删除文件与路径越界做严格检查。这是 impact 命令"输入确定性"
// 的保障——旧快照、空 diff、未应用 diff 或越界路径都应直接失败。
package diff

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateApplied 校验 changes 描述的变更是否与当前项目快照一致。
// 删除文件必须已不存在；其余文件按 ExpectedLines 逐行比对磁盘内容（CRLF 归一为 LF）。
// 任何不一致都返回错误，描述为 "does not match the post-change source"。
func ValidateApplied(root string, changes []FileChange) error {
	for _, change := range changes {
		// 删除文件取旧路径作为校验对象，其余取新路径。
		path := change.NewPath
		if change.Status == StatusDeleted {
			path = change.OldPath
		}
		fullPath, err := projectPath(root, path)
		if err != nil {
			return err
		}

		if change.Status == StatusDeleted {
			// 删除文件在变更后源码中必须已不存在；存在则说明快照不匹配。
			if _, err := os.Stat(fullPath); err == nil {
				return fmt.Errorf("diff for %q does not match the post-change source: deleted file still exists", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				// 非"文件不存在"的其他错误（如权限问题）单独上报。
				return fmt.Errorf("validate deleted file %q: %w", path, err)
			}
			continue
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			// 读不到文件说明 diff 与快照不一致（文件应存在但缺失）。
			return fmt.Errorf("diff for %q does not match the post-change source: %w", path, err)
		}
		// CRLF 归一为 LF 后按行拆分，与解析 diff 时保持一致的行口径。
		lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
		for _, expected := range change.ExpectedLines {
			// 行号越界或内容不符都视为快照不匹配。
			if expected.Line <= 0 || expected.Line > len(lines) || lines[expected.Line-1] != expected.Text {
				return fmt.Errorf(
					"diff for %q does not match the post-change source at line %d",
					path,
					expected.Line,
				)
			}
		}
		// 纯删除区域没有正面证据证明其已被应用：ExpectedLine 只覆盖新增行与保留的
		// 上下文行。当一个删除块紧随其后有上下文行时，该上下文行会成为 ExpectedLine，
		// 未应用会在那里被 ExpectedLine 校验捕获；但若删除块位于文件末尾（如 git diff -U3
		// 删除尾部函数，只有前导上下文、无尾随上下文）或使用 -U0（无任何上下文），
		// 就没有 ExpectedLine 守卫该区域，未应用的删除会被漏判为已应用。
		//
		// 因此对“其新版本锚点未被任何 ExpectedLine 覆盖”的删除块补充校验：被删块若仍
		// 原样出现在旧版本起始行处，说明删除尚未应用。用锚点是否被 ExpectedLine 覆盖来
		// 门控，既补齐了 EOF/-U0 缺口，又避免对带尾随上下文的常规删除做多余（且对
		// 重复行可能误判的）逐字比对。
		expectedAtLine := map[int]bool{}
		for _, expected := range change.ExpectedLines {
			expectedAtLine[expected.Line] = true
		}
		for _, block := range change.DeletedBlocks {
			if expectedAtLine[block.NewAnchorLine] {
				continue
			}
			if deletedBlockStillPresent(lines, block) {
				return fmt.Errorf(
					"diff for %q does not match the post-change source: deleted lines still present near line %d",
					path,
					block.OldStartLine,
				)
			}
		}
	}
	return nil
}

// deletedBlockStillPresent 判断被删除块是否仍完整、连续地出现在其旧版本起始行处，
// 用于识别未应用的纯删除 diff：未应用时文件仍是旧布局，被删行恰好落在 OldStartLine。
// 要求整块逐行匹配，避免因单行 trivial 内容（如相邻重复的 "}"）产生误判；
// 空块视为不成立。
func deletedBlockStillPresent(lines []string, block DeletedBlock) bool {
	if len(block.Lines) == 0 || block.OldStartLine <= 0 {
		return false
	}
	start := block.OldStartLine - 1
	if start+len(block.Lines) > len(lines) {
		return false
	}
	for i, deleted := range block.Lines {
		if lines[start+i] != deleted {
			return false
		}
	}
	return true
}

// projectPath 把 diff 中的相对路径解析为项目根下的绝对路径，并做安全校验：
// 拒绝空路径、绝对路径以及以 ".." 逃逸出项目根的路径，避免越界读取项目外文件。
func projectPath(root, path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	// 拒绝空路径、绝对路径，以及清理后等于 ".." 或以 "../" 开头的相对逃逸路径。
	if path == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe diff path %q", path)
	}
	return filepath.Join(root, clean), nil
}
