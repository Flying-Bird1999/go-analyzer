package main

import (
	"bytes"
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

func TestFactsRejectsRelativeConfigPath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "mini-bff"))
	if err != nil {
		t.Fatal(err)
	}
	err = run([]string{"facts", "--project", root, "--config", "go-analyzer.json", "--format", "json"})
	if err == nil {
		t.Fatal("expected relative config path to fail")
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
	out, err := runWithCapturedStdoutBytes(t, []string{"impact", "--project", root, "--diff", diffPath, "--format", "json"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"schemaVersion": "go-impact/v1alpha1"`)) {
		t.Fatalf("impact output = %s", out)
	}
	if !bytes.Contains(out, []byte(`"fileSources"`)) {
		t.Fatalf("impact output = %s", out)
	}
}

func TestSchemaCommandWritesFactsSchema(t *testing.T) {
	out, err := runWithCapturedStdoutBytes(t, []string{"schema", "--type", "facts"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"title": "go-analyzer facts output"`)) {
		t.Fatalf("schema output = %s", out)
	}
}

func TestHelpCommandListsCommands(t *testing.T) {
	out, err := runWithCapturedStdoutBytes(t, []string{"help"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"facts", "impact", "schema", "absolute paths"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("help output missing %q: %s", want, out)
		}
	}
}

func runWithCapturedStdoutBytes(t *testing.T, args []string) ([]byte, error) {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	done := make(chan error, 1)
	var buf bytes.Buffer
	go func() {
		_, err := io.Copy(&buf, reader)
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
	return buf.Bytes(), runErr
}
