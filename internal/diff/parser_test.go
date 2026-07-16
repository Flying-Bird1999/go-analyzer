// parser_test.go 验证 unified diff 解析器：多文件变更、原始 patch 保留、删除块与锚点、
// 期望行、git quoted 路径与空输入拒绝等行为。

package diff

import (
	"strings"
	"testing"
)

// TestParseUnifiedDiffChangedNewFileRanges 验证多文件 diff 能解析出正确的路径、状态与新版本行范围。
func TestParseUnifiedDiffChangedNewFileRanges(t *testing.T) {
	input := []byte(`diff --git a/controller/common.go b/controller/common.go
index 1111111..2222222 100644
--- a/controller/common.go
+++ b/controller/common.go
@@ -10,6 +10,8 @@ func CheckIn() {
 	existing()
+	added()
+	changed()
 	done()
diff --git a/router/router.go b/router/router.go
index 3333333..4444444 100644
--- a/router/router.go
+++ b/router/router.go
@@ -20,3 +20,4 @@ func InitRouter() {
 	group.GET("/a", h)
+	group.POST("/b", h)
 }
`)

	changes, err := ParseUnified(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("file changes = %d", len(changes))
	}
	first := changes[0]
	if first.OldPath != "controller/common.go" || first.NewPath != "controller/common.go" {
		t.Fatalf("paths = %#v", first)
	}
	if first.Status != StatusModified {
		t.Fatalf("status = %q", first.Status)
	}
	if len(first.Ranges) != 1 {
		t.Fatalf("ranges = %#v", first.Ranges)
	}
	if first.Ranges[0].StartLine != 11 || first.Ranges[0].EndLine != 12 {
		t.Fatalf("first range = %#v", first.Ranges[0])
	}
	second := changes[1]
	if second.NewPath != "router/router.go" {
		t.Fatalf("second path = %q", second.NewPath)
	}
	if second.Ranges[0].StartLine != 21 || second.Ranges[0].EndLine != 21 {
		t.Fatalf("second range = %#v", second.Ranges[0])
	}
}

// TestParseUnifiedPreservesRawFilePatch 验证每个文件保留独立的原始 patch 文本，且不串入下一个文件的内容。
func TestParseUnifiedPreservesRawFilePatch(t *testing.T) {
	input := []byte(`diff --git a/controller/common.go b/controller/common.go
index 1111111..2222222 100644
--- a/controller/common.go
+++ b/controller/common.go
@@ -10,3 +10,4 @@ func CheckIn() {
 existing()
+	added()
 }
diff --git a/router/router.go b/router/router.go
index 3333333..4444444 100644
--- a/router/router.go
+++ b/router/router.go
@@ -20,3 +20,4 @@ func InitRouter() {
 group.GET("/a", h)
+	group.POST("/b", h)
 }
`)

	changes, err := ParseUnified(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("file changes = %d", len(changes))
	}
	if !strings.HasPrefix(changes[0].Raw, "diff --git a/controller/common.go b/controller/common.go\n") {
		t.Fatalf("first raw patch = %q", changes[0].Raw)
	}
	if !strings.Contains(changes[0].Raw, "+\tadded()\n") {
		t.Fatalf("first raw patch does not contain added line: %q", changes[0].Raw)
	}
	if strings.Contains(changes[0].Raw, "router/router.go") {
		t.Fatalf("first raw patch contains next file: %q", changes[0].Raw)
	}
	if !strings.HasSuffix(changes[1].Raw, " }\n") {
		t.Fatalf("second raw patch = %q", changes[1].Raw)
	}
}

// TestParseUnifiedCreatesDeletionOnlyAnchorOnNewSide 验证纯删除 hunk 在新版本侧生成 deletion_anchor 行范围。
func TestParseUnifiedCreatesDeletionOnlyAnchorOnNewSide(t *testing.T) {
	input := []byte(`diff --git a/model/order.go b/model/order.go
index 1111111..2222222 100644
--- a/model/order.go
+++ b/model/order.go
@@ -12,3 +12,2 @@ type Order struct {
 ID string
-	LegacyCode string
 }
`)

	changes, err := ParseUnified(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || len(changes[0].Ranges) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	got := changes[0].Ranges[0]
	if got.StartLine != 13 || got.EndLine != 13 || got.Kind != RangeKindDeletionAnchor {
		t.Fatalf("deletion anchor = %#v", got)
	}
}

// TestParseUnifiedPreservesDeletedBlocks 验证连续删除块的旧行号、新版本锚点行号与原文被完整保留。
func TestParseUnifiedPreservesDeletedBlocks(t *testing.T) {
	input := []byte("diff --git a/router/router.go b/router/router.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/router/router.go\n" +
		"+++ b/router/router.go\n" +
		"@@ -20,5 +20,3 @@ func InitRouter() {\n" +
		" \tgroup.GET(\"/a\", aHandler)\n" +
		"-\tgroup.POST(\"/legacy\", legacyHandler)\n" +
		"-\tgroup.DELETE(\"/legacy/:id\", deleteLegacyHandler)\n" +
		" \tgroup.GET(\"/b\", bHandler)\n" +
		"@@ -42,3 +40,3 @@ func InitRouter() {\n" +
		"-\toldMiddleware()\n" +
		"+\tnewMiddleware()\n" +
		" }\n")

	changes, err := ParseUnified(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("file changes = %d", len(changes))
	}
	got := changes[0].DeletedBlocks
	if len(got) != 2 {
		t.Fatalf("deleted blocks = %#v", got)
	}
	first := got[0]
	if first.OldStartLine != 21 || first.NewAnchorLine != 21 {
		t.Fatalf("first deleted block location = %#v", first)
	}
	if want := []string{"\tgroup.POST(\"/legacy\", legacyHandler)", "\tgroup.DELETE(\"/legacy/:id\", deleteLegacyHandler)"}; !equalStrings(first.Lines, want) {
		t.Fatalf("first deleted block lines = %#v, want %#v", first.Lines, want)
	}
	second := got[1]
	if second.OldStartLine != 42 || second.NewAnchorLine != 40 {
		t.Fatalf("replacement deleted block location = %#v", second)
	}
	if want := []string{"\toldMiddleware()"}; !equalStrings(second.Lines, want) {
		t.Fatalf("replacement deleted block lines = %#v, want %#v", second.Lines, want)
	}
}

// TestParseUnifiedRetainsExpectedPostChangeLines 验证解析保留了变更后期望出现的行内容，供 ValidateApplied 校验。
func TestParseUnifiedRetainsExpectedPostChangeLines(t *testing.T) {
	input := []byte("diff --git a/service/a.go b/service/a.go\n" +
		"--- a/service/a.go\n" +
		"+++ b/service/a.go\n" +
		"@@ -1,3 +1,3 @@\n" +
		" package service\n" +
		"-const Value = \"old\"\n" +
		"+const Value = \"new\"\n" +
		" \n")

	changes, err := ParseUnified(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	got := changes[0].ExpectedLines
	want := []ExpectedLine{
		{Line: 1, Text: "package service"},
		{Line: 2, Text: `const Value = "new"`},
		{Line: 3, Text: ""},
	}
	if len(got) != len(want) {
		t.Fatalf("expected lines = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected line %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestParseUnifiedUnquotesGitQuotedUTF8Paths 验证 git quoted（八进制转义）的 UTF-8 文件名能被还原。
func TestParseUnifiedUnquotesGitQuotedUTF8Paths(t *testing.T) {
	input := []byte("diff --git \"a/docs/design/SC1-3352/SC1-3352-\\346\\216\\245\\345\\217\\243\\350\\277\\201\\347\\247\\273\\350\\256\\276\\350\\256\\241.md\" \"b/docs/design/SC1-3352/SC1-3352-\\346\\216\\245\\345\\217\\243\\350\\277\\201\\347\\247\\273\\350\\256\\276\\350\\256\\241.md\"\n" +
		"index 1111111..2222222 100644\n" +
		"--- \"a/docs/design/SC1-3352/SC1-3352-\\346\\216\\245\\345\\217\\243\\350\\277\\201\\347\\247\\273\\350\\256\\276\\350\\256\\241.md\"\n" +
		"+++ \"b/docs/design/SC1-3352/SC1-3352-\\346\\216\\245\\345\\217\\243\\350\\277\\201\\347\\247\\273\\350\\256\\276\\350\\256\\241.md\"\n" +
		"@@ -1,2 +1,2 @@\n" +
		" title\n" +
		"-old\n" +
		"+new\n")

	changes, err := ParseUnified(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	want := "docs/design/SC1-3352/SC1-3352-接口迁移设计.md"
	if changes[0].OldPath != want || changes[0].NewPath != want {
		t.Fatalf("paths = old %q new %q, want %q", changes[0].OldPath, changes[0].NewPath, want)
	}
}

// TestParseUnifiedRejectsEmptyInput 验证空 diff 直接报错。
func TestParseUnifiedRejectsEmptyInput(t *testing.T) {
	if _, err := ParseUnified(nil); err == nil {
		t.Fatal("expected empty diff to be rejected")
	}
}

// TestParseUnifiedBinaryPatch 验证 GIT binary patch 与 Binary files differ 行
// 不会被当作内容行解析（P1-5 回归保护）。
//
// 修复前：base85 字母表含 +/-，base85 行会被当成新增/删除行，污染 ExpectedLines
// 与 DeletedBlocks，最终在 ValidateApplied 阶段报误导性的 line 0 不匹配。
// 修复后：binary patch 头关闭 hunk 解析，base85 行被忽略；路径头保留。
func TestParseUnifiedBinaryPatch(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"gitBinaryPatch", "GIT binary patch"},
		{"binaryFilesDiffer", "Binary files a/bin and b/bin differ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte("diff --git a/bin b/bin\n" +
				"index 1111111..2222222 100644\n" +
				tc.header + "\n" +
				"delta abc+def-ghi\n" +
				"more+base85-data\n")
			changes, err := ParseUnified(input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(changes) != 1 {
				t.Fatalf("file changes = %d", len(changes))
			}
			change := changes[0]
			if change.OldPath != "bin" || change.NewPath != "bin" {
				t.Fatalf("paths = %#v, want bin/bin (path header must survive binary patch)", change)
			}
			// base85 行（以 +/- 起首）不应被当成新增/删除内容。
			for _, el := range change.ExpectedLines {
				if el.Line <= 0 {
					t.Errorf("binary patch produced bogus ExpectedLine at line %d: %q", el.Line, el.Text)
				}
			}
			if len(change.Ranges) != 0 {
				t.Errorf("binary patch should produce no added ranges, got %#v", change.Ranges)
			}
			if len(change.DeletedBlocks) != 0 {
				t.Errorf("binary patch should produce no deleted blocks, got %#v", change.DeletedBlocks)
			}
		})
	}
}

// TestParseUnifiedDoubleDashInHunk 验证 hunk 内以 `-- ` 起首的删除行（如 SQL 注释
// `-- DROP TABLE users`）不会被误判为 `--- ` 路径头（P1-5 回归保护）。
//
// 修复前：`-` + 原文 `-- DROP TABLE users` = `--- DROP TABLE users` 命中 `--- ` 分支，
// OldPath 被覆盖为 "DROP TABLE users"，且 oldLine 不递增导致后续行号偏移。
// 修复后：`--- `/`+++ ` 仅在 !hunkActive 时识别，hunk 内 `-- ` 行正确当作删除行。
func TestParseUnifiedDoubleDashInHunk(t *testing.T) {
	input := []byte("diff --git a/sql.go b/sql.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/sql.go\n" +
		"+++ b/sql.go\n" +
		"@@ -1,4 +1,3 @@\n" +
		" package sql\n" +
		" \n" +
		"-- const query = `-- DROP TABLE users`\n" +
		" \n" +
		" const other = 1\n")
	changes, err := ParseUnified(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("file changes = %d", len(changes))
	}
	change := changes[0]
	// 修复前：OldPath 被污染为 "const query = `-- DROP TABLE users`"
	if change.OldPath != "sql.go" || change.NewPath != "sql.go" {
		t.Fatalf("paths = %#v, want sql.go/sql.go (hunk content must not override path header)", change)
	}
	// 删除行 `- const query = ...`（原文 `- const query = ...`）应进入 DeletedBlocks。
	if len(change.DeletedBlocks) != 1 {
		t.Fatalf("deleted blocks = %#v, want 1", change.DeletedBlocks)
	}
	block := change.DeletedBlocks[0]
	if !equalStrings(block.Lines, []string{"- const query = `-- DROP TABLE users`"}) {
		t.Fatalf("deleted block lines = %#v", block.Lines)
	}
}

// equalStrings 判断两个字符串切片是否逐元素相等，供删除块行断言使用。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
