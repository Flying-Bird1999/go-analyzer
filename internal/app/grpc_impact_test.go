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
			Grpc []struct {
				Kind     string `json:"kind"`
				Identity string `json:"identity"`
			} `json:"grpc"`
			Dubbo []struct {
				Kind     string `json:"kind"`
				Identity string `json:"identity"`
			} `json:"dubbo"`
			HTTP []struct {
				Kind     string `json:"kind"`
				Identity string `json:"identity"`
			} `json:"http"`
			Job []struct {
				Kind     string `json:"kind"`
				Identity string `json:"identity"`
			} `json:"job"`
		} `json:"summary"`
		FileSources []struct {
			SourceFile string         `json:"sourceFile"`
			Symbols    map[string]any `json:"symbols"`
		} `json:"fileSources"`
		EntrySourcesSummary struct {
			Grpc []struct {
				Sources []struct {
					Chains [][]string `json:"chains"`
				} `json:"sources"`
			} `json:"grpc"`
			Dubbo []any `json:"dubbo"`
			HTTP  []any `json:"http"`
			Job   []any `json:"job"`
		} `json:"entrySourcesSummary"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Summary.Grpc) != 1 {
		t.Fatalf("summary = %#v\n%s", doc.Summary, out)
	}
	contracts := append(append(append(append([]struct {
		Kind     string `json:"kind"`
		Identity string `json:"identity"`
	}{}, doc.Summary.Grpc...), doc.Summary.Dubbo...), doc.Summary.HTTP...), doc.Summary.Job...)
	if len(contracts) != 4 {
		t.Fatalf("contract summary = %#v\n%s", doc.Summary, out)
	}
	wantKinds := map[string]bool{"grpc_operation": true, "http_endpoint": true, "dubbo_method": true, "job": true}
	for _, contract := range contracts {
		delete(wantKinds, contract.Kind)
		if contract.Identity == "GET /internal/other" || contract.Identity == "other" || strings.HasSuffix(contract.Identity, "/other") {
			t.Fatalf("unaffected sibling contract leaked: %#v\n%s", contract, out)
		}
	}
	if len(wantKinds) != 0 {
		t.Fatalf("missing contract kinds = %#v\n%s", wantKinds, out)
	}
	const want = "/example.echo.v1.EchoService/ping"
	if doc.Summary.Grpc[0].Identity != want {
		t.Fatalf("full method = %q, want %q", doc.Summary.Grpc[0].Identity, want)
	}
	if len(doc.FileSources) != 1 || doc.FileSources[0].SourceFile != "service/reply.go" || len(doc.FileSources[0].Symbols) == 0 {
		t.Fatalf("file sources = %#v", doc.FileSources)
	}
	if len(doc.EntrySourcesSummary.Grpc) != 1 || len(doc.EntrySourcesSummary.Grpc[0].Sources) != 1 || len(doc.EntrySourcesSummary.Grpc[0].Sources[0].Chains) == 0 ||
		len(doc.EntrySourcesSummary.Dubbo) != 1 || len(doc.EntrySourcesSummary.HTTP) != 1 || len(doc.EntrySourcesSummary.Job) != 1 {
		t.Fatalf("entry sources summary = %#v\n%s", doc.EntrySourcesSummary, out)
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
			Grpc []struct {
				FullMethod string `json:"fullMethod"`
			} `json:"grpc"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Summary.Grpc) != 1 || doc.Summary.Grpc[0].FullMethod != "/example.echo.v1.EchoService/ping" {
		t.Fatalf("operations = %#v\n%s", doc.Summary.Grpc, out)
	}
}

func TestGrpcImpactMapsDubboServiceConfigChangeToAllMethods(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "grpc-service"))
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(t.TempDir(), "change.diff")
	patch := []byte(strings.Join([]string{
		"diff --git a/provider/dubbo.go b/provider/dubbo.go",
		"--- a/provider/dubbo.go",
		"+++ b/provider/dubbo.go",
		"@@ -13,4 +13,4 @@ func ExportReplyAPI() {",
		" \tdubbox.GetRootConfig().Provider.Services[\"ReplyAPI\"] = &dubbox.ServiceConfig{",
		" \t\tInterface: \"example.reply.ReplyAPI\",",
		"-\t\tVersion:   \"0.9.0\",",
		"+\t\tVersion:   \"1.0.0\",",
		" \t\tMethods: []*dubbox.MethodConfig{",
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
			Dubbo []struct {
				Kind   string `json:"kind"`
				Method string `json:"dubboMethod"`
			} `json:"dubbo"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"reply": true, "other": true}
	for _, contract := range doc.Summary.Dubbo {
		if contract.Kind == "dubbo_method" {
			delete(want, contract.Method)
		}
	}
	if len(want) != 0 || len(doc.Summary.Dubbo) != 2 {
		t.Fatalf("contracts = %#v\n%s", doc.Summary.Dubbo, out)
	}
}
