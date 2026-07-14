package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrpcImpactMapsBusinessChangeToRegisteredOperation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "grpc-service"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	patch := []byte(`diff --git a/service/reply.go b/service/reply.go
--- a/service/reply.go
+++ b/service/reply.go
@@ -3,5 +3,5 @@ package service
 func BuildReply() string {
-	return "old"
+	return "pong"
 }
`)
	if err := os.WriteFile(diffPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := RunGrpcImpact(GrpcImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Summary struct {
			Count      int `json:"impactedGrpcOperationCount"`
			Operations []struct {
				FullMethod string `json:"fullMethod"`
			} `json:"impactedGrpcOperations"`
		} `json:"summary"`
		FileSources []struct {
			SourceFile string         `json:"sourceFile"`
			Symbols    map[string]any `json:"symbols"`
		} `json:"fileSources"`
		SourcesSummary []struct {
			Grpc struct {
				FullMethod string `json:"fullMethod"`
			} `json:"grpc"`
			Sources []struct {
				Chains [][]string `json:"chains"`
			} `json:"sources"`
		} `json:"grpcOperationSourcesSummary"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Summary.Count != 1 || len(doc.Summary.Operations) != 1 {
		t.Fatalf("summary = %#v\n%s", doc.Summary, out)
	}
	const want = "/example.echo.v1.EchoService/ping"
	if doc.Summary.Operations[0].FullMethod != want {
		t.Fatalf("full method = %q, want %q", doc.Summary.Operations[0].FullMethod, want)
	}
	if len(doc.FileSources) != 1 || doc.FileSources[0].SourceFile != "service/reply.go" || len(doc.FileSources[0].Symbols) == 0 {
		t.Fatalf("file sources = %#v", doc.FileSources)
	}
	if len(doc.SourcesSummary) != 1 || doc.SourcesSummary[0].Grpc.FullMethod != want || len(doc.SourcesSummary[0].Sources) != 1 || len(doc.SourcesSummary[0].Sources[0].Chains) == 0 {
		t.Fatalf("sources summary = %#v", doc.SourcesSummary)
	}
}

func TestGrpcImpactMapsGeneratedRequestChangeToOnlyUsingOperation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "grpc-service"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	patch := []byte(strings.Join([]string{
		"diff --git a/api/echo.pb.go b/api/echo.pb.go",
		"--- a/api/echo.pb.go",
		"+++ b/api/echo.pb.go",
		"@@ -4,5 +4,5 @@ package api",
		" ",
		" type PingRequest struct {",
		"-\tValue int",
		"+\tValue string",
		" }",
		"",
	}, "\n"))
	if err := os.WriteFile(diffPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := RunGrpcImpact(GrpcImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Summary struct {
			Operations []struct {
				FullMethod string `json:"fullMethod"`
			} `json:"impactedGrpcOperations"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Summary.Operations) != 1 || doc.Summary.Operations[0].FullMethod != "/example.echo.v1.EchoService/ping" {
		t.Fatalf("operations = %#v\n%s", doc.Summary.Operations, out)
	}
}
