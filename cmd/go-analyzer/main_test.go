package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFactsRejectsRelativeProjectPath(t *testing.T) {
	err := run([]string{"facts", "--project", "testdata/fixtures/mini-bff", "--format", "json"})
	if err == nil {
		t.Fatal("expected relative project path to fail")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestImpactRejectsRelativeDiffPath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "utility-fanout"))
	if err != nil {
		t.Fatal(err)
	}
	err = run([]string{"impact", "--project", root, "--diff", "change.diff", "--format", "json"})
	if err == nil {
		t.Fatal("expected relative diff path to fail")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestImpactAcceptsAbsolutePaths(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "utility-fanout"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	diff := []byte(`diff --git a/service/common.go b/service/common.go
index 1111111..2222222 100644
--- a/service/common.go
+++ b/service/common.go
@@ -2,3 +2,4 @@ package service
func CheckIn() string {
+	_ = "changed"
    return "ok"
 }
`)
	if err := os.WriteFile(diffPath, diff, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runWithCapturedStdout(t, []string{"impact", "--project", root, "--diff", diffPath, "--format", "json"}); err != nil {
		t.Fatal(err)
	}
}

func runWithCapturedStdout(t *testing.T, args []string) error {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, reader)
		done <- err
	}()
	defer func() {
		os.Stdout = original
	}()

	runErr := run(args)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return runErr
}
