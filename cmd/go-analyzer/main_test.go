package main

import (
	"bytes"
	"encoding/json"
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

func TestFactsAcceptsExplicitBuildContextFlags(t *testing.T) {
	root := t.TempDir()
	writeMainTestFile(t, root, "go.mod", "module example.com/cli-build-context\n\ngo 1.24\n")
	writeMainTestFile(t, root, "default.go", `//go:build !customtag

package clibuildcontext

func DefaultOnly() {}
`)
	writeMainTestFile(t, root, "tagged.go", `//go:build customtag

package clibuildcontext

func TaggedOnly() {}
`)

	out, err := runWithCapturedStdoutBytes(t, []string{
		"facts",
		"--project", root,
		"--tags", "customtag",
		"--goos", "linux",
		"--goarch", "amd64",
		"--cgo", "false",
		"--format", "json",
	})
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Project struct {
			BuildContext struct {
				GOOS       string   `json:"goos"`
				GOARCH     string   `json:"goarch"`
				Tags       []string `json:"tags"`
				CgoEnabled bool     `json:"cgo_enabled"`
			} `json:"build_context"`
		} `json:"project"`
		Symbols []struct {
			Name string `json:"name"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Project.BuildContext.GOOS != "linux" || doc.Project.BuildContext.GOARCH != "amd64" || doc.Project.BuildContext.CgoEnabled {
		t.Fatalf("build context = %#v", doc.Project.BuildContext)
	}
	if len(doc.Project.BuildContext.Tags) != 1 || doc.Project.BuildContext.Tags[0] != "customtag" {
		t.Fatalf("tags = %#v", doc.Project.BuildContext.Tags)
	}
	for _, symbol := range doc.Symbols {
		if symbol.Name == "DefaultOnly" {
			t.Fatalf("default-only symbol loaded under customtag: %#v", doc.Symbols)
		}
	}
}

func TestFactsTimingsWritesPipelineStagesToStderr(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "mini-bff"))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runWithCapturedOutputBytes(t, []string{"facts", "--project", root, "--timings", "--format", "json"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stdout, []byte(`"project"`)) {
		t.Fatalf("stdout = %s", stdout)
	}
	for _, want := range []string{"timing project_load=", "timing reference_extract="} {
		if !bytes.Contains(stderr, []byte(want)) {
			t.Fatalf("stderr missing %q: %s", want, stderr)
		}
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

func TestImpactRejectsRelativeImpactConfigPath(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "utility-fanout"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	if err := os.WriteFile(diffPath, []byte(`diff --git a/service/common.go b/service/common.go
--- a/service/common.go
+++ b/service/common.go
@@ -3,3 +3,3 @@ package service
 func CheckIn() string {
-	return "before"
+	return "ok"
 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"impact", "--project", root, "--diff", diffPath, "--impact-config", ".analyzer/go-impact.config.json", "--format", "json"})
	if err == nil {
		t.Fatal("expected relative impact config path to fail")
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
@@ -3,3 +3,3 @@ package service
 func CheckIn() string {
-	return "before"
+	return "ok"
 }
`)
	if err := os.WriteFile(diffPath, diff, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runWithCapturedStdoutBytes(t, []string{"impact", "--project", root, "--diff", diffPath, "--format", "json"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"fileSources"`)) {
		t.Fatalf("impact output = %s", out)
	}
	if bytes.Contains(out, []byte(`"schemaVersion"`)) {
		t.Fatalf("impact output should not expose schemaVersion: %s", out)
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

func TestGrpcImpactCommandWritesCanonicalOperation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "grpc-service"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	writeMainTestFile(t, filepath.Dir(diffPath), filepath.Base(diffPath), `diff --git a/service/reply.go b/service/reply.go
--- a/service/reply.go
+++ b/service/reply.go
@@ -3,5 +3,5 @@ package service
 func BuildReply() string {
-	return "old"
+	return "pong"
 }
`)
	out, err := runWithCapturedStdoutBytes(t, []string{"grpc-impact", "--project", root, "--diff", diffPath, "--format", "json"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"fullMethod": "/example.echo.v1.EchoService/ping"`)) {
		t.Fatalf("grpc-impact output = %s", out)
	}
	if bytes.Contains(out, []byte(`/example.echo.v1.EchoService/Health`)) {
		t.Fatalf("grpc-impact should not include unaffected operation: %s", out)
	}
}

func TestSchemaCommandWritesGrpcImpactSchema(t *testing.T) {
	out, err := runWithCapturedStdoutBytes(t, []string{"schema", "--type", "grpc-impact"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"title": "go-analyzer gRPC provider impact tree"`)) {
		t.Fatalf("schema output = %s", out)
	}
}

func TestHelpCommandListsCommands(t *testing.T) {
	out, err := runWithCapturedStdoutBytes(t, []string{"help"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"impact", "grpc-impact", "HTTP 接口", "IM event", "绝对路径"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("help output missing %q: %s", want, out)
		}
	}
	for _, hidden := range []string{"facts", "schema"} {
		if bytes.Contains(out, []byte(hidden)) {
			t.Fatalf("default help should not mention internal command %q: %s", hidden, out)
		}
	}
}

func TestImpactHelpMentionsEndpointAndIMEvents(t *testing.T) {
	out, err := runWithCapturedStdoutBytes(t, []string{"help", "impact"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HTTP 接口", "IM event", "unified diff", "变更后源码", "--impact-config"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("impact help output missing %q: %s", want, out)
		}
	}
}

func runWithCapturedStdoutBytes(t *testing.T, args []string) ([]byte, error) {
	stdout, _, err := runWithCapturedOutputBytes(t, args)
	return stdout, err
}

func runWithCapturedOutputBytes(t *testing.T, args []string) ([]byte, []byte, error) {
	t.Helper()
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	stdoutDone := make(chan error, 1)
	stderrDone := make(chan error, 1)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	go func() {
		_, err := io.Copy(&stdoutBuf, stdoutReader)
		stdoutDone <- err
	}()
	go func() {
		_, err := io.Copy(&stderrBuf, stderrReader)
		stderrDone <- err
	}()
	defer func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	}()

	runErr := run(args)
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-stdoutDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stderrDone; err != nil {
		t.Fatal(err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatal(err)
	}
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), runErr
}

func writeMainTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
