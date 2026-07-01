package diff

import (
	"strings"
	"testing"
)

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

func TestParseUnifiedRejectsEmptyInput(t *testing.T) {
	if _, err := ParseUnified(nil); err == nil {
		t.Fatal("expected empty diff to be rejected")
	}
}

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
