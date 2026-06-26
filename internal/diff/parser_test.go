package diff

import "testing"

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
