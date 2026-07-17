// validate_test.go 验证 ValidateApplied：接受匹配的变更后源码、拒绝旧快照、拒绝越界路径。

package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateAppliedAcceptsPostChangeSource 验证期望行与磁盘内容一致时校验通过。
func TestValidateAppliedAcceptsPostChangeSource(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "service/a.go", "package service\nconst Value = \"new\"\n")

	err := ValidateApplied(root, []FileChange{{
		NewPath: "service/a.go",
		Status:  StatusModified,
		ExpectedLines: []ExpectedLine{
			{Line: 1, Text: "package service"},
			{Line: 2, Text: `const Value = "new"`},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// TestValidateAppliedRejectsPreChangeSource 验证旧快照（内容不匹配）时校验失败。
func TestValidateAppliedRejectsPreChangeSource(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "service/a.go", "package service\nconst Value = \"old\"\n")

	err := ValidateApplied(root, []FileChange{{
		NewPath: "service/a.go",
		Status:  StatusModified,
		ExpectedLines: []ExpectedLine{
			{Line: 2, Text: `const Value = "new"`},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "does not match the post-change source") {
		t.Fatalf("error = %v", err)
	}
}

// TestValidateAppliedRejectsUnappliedTailDeletion 验证带前导上下文的“文件末尾删除”
// diff（git diff -U3 删除尾部函数：只有前导上下文、无尾随上下文）在未应用时被拒绝。
// 修复前该场景因存在（前导上下文的）ExpectedLine 而跳过删除块校验，未应用的删除被
// 误判为已应用，MapChanges 会把变更归因到错误符号。
func TestValidateAppliedRejectsUnappliedTailDeletion(t *testing.T) {
	// -U3 风格：3 行前导上下文 + 尾部 func B 的删除块，无尾随上下文。
	patch := "diff --git a/svc/a.go b/svc/a.go\n" +
		"--- a/svc/a.go\n" +
		"+++ b/svc/a.go\n" +
		"@@ -2,5 +2,2 @@\n" +
		" func A() {\n" +
		" \tdoA()\n" +
		" }\n" +
		"-func B() {\n" +
		"-\tdoB()\n" +
		"-}\n"
	changes, err := ParseUnified([]byte(patch))
	if err != nil {
		t.Fatal(err)
	}
	preChange := "package svc\nfunc A() {\n\tdoA()\n}\nfunc B() {\n\tdoB()\n}\n"
	postChange := "package svc\nfunc A() {\n\tdoA()\n}\n"

	root := t.TempDir()
	writeValidationFile(t, root, "svc/a.go", preChange)
	if err := ValidateApplied(root, changes); err == nil || !strings.Contains(err.Error(), "does not match the post-change source") {
		t.Fatalf("unapplied tail deletion: error = %v, want post-change mismatch", err)
	}

	appliedRoot := t.TempDir()
	writeValidationFile(t, appliedRoot, "svc/a.go", postChange)
	if err := ValidateApplied(appliedRoot, changes); err != nil {
		t.Fatalf("applied tail deletion should pass: %v", err)
	}
}

// TestValidateAppliedRejectsPathOutsideProject 验证 "../" 越界路径被安全校验拒绝。
func TestValidateAppliedRejectsPathOutsideProject(t *testing.T) {
	err := ValidateApplied(t.TempDir(), []FileChange{{
		NewPath: "../outside.go",
		Status:  StatusModified,
	}})
	if err == nil || !strings.Contains(err.Error(), "unsafe diff path") {
		t.Fatalf("error = %v", err)
	}
}

// writeValidationFile 在测试临时目录下写入指定相对路径的文件，自动创建父目录。
func writeValidationFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
