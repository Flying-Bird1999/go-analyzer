package im

import (
	"go/ast"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
)

func TestCommonSDKAdapterMatchesExactImportedSymbols(t *testing.T) {
	_, _, file := loadEvaluatorProject(t, `package sample

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

func send(ctx any, event string, payload any) {
	notifyim.SendIm(ctx, "app", "group", event, payload)
	notifyim.SendImAsync(ctx, "app", "group", event, payload, nil)
	notifyim.SendImToUid(ctx, "app", []string{"u"}, event, payload)
	notifyim.SendImToUidAsync(ctx, "app", []string{"u"}, event, payload, nil)
}
`)
	calls := callExpressions(functionDecl(t, file, "send"))
	if len(calls) != 4 {
		t.Fatalf("calls = %d", len(calls))
	}
	for _, call := range calls {
		match, ok := matchSDKCall(file, call)
		if !ok {
			t.Fatalf("SDK call not matched: %#v", call.Fun)
		}
		if match.EventArg != 3 || match.PayloadArg != 4 {
			t.Fatalf("arguments = %#v", match)
		}
	}
}

func TestCommonSDKAdapterRejectsSameNameFromAnotherPackage(t *testing.T) {
	_, _, file := loadEvaluatorProject(t, `package sample

import fakeim "example.com/fake/im"

func send(ctx any, event string, payload any) {
	fakeim.SendIm(ctx, "app", "group", event, payload)
}
`)
	calls := callExpressions(functionDecl(t, file, "send"))
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if _, ok := matchSDKCall(file, calls[0]); ok {
		t.Fatal("same-named function from another package matched")
	}
}

func TestSDKArgumentMismatchEmitsDiagnostic(t *testing.T) {
	// A known SDK function invoked with too few arguments to carry the
	// expected event/payload positions must not be silently dropped; it
	// should surface as an im_sdk_argument_mismatch diagnostic.
	p, idx, store := loadIMProject(t, map[string]string{
		"sender/sender.go": `package sender

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

func Send(ctx any) {
	notifyim.SendIm(ctx, "app")
}
`,
	})
	if err := Extract(p, idx, store); err != nil {
		t.Fatalf("extract: %v", err)
	}
	var found int
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == string(diagnostics.CodeIMSDKArgumentMismatch) {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("im_sdk_argument_mismatch diagnostics = %d, want 1 (diagnostics=%v)", found, store.Diagnostics)
	}
}

func TestValidSDKCallEmitsNoArgumentMismatch(t *testing.T) {
	// A well-formed SDK call must not trigger the drift diagnostic.
	p, idx, store := loadIMProject(t, map[string]string{
		"sender/sender.go": `package sender

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

func Send(ctx any, payload any) {
	notifyim.SendIm(ctx, "app", "group", "SOME_EVENT", payload)
}
`,
	})
	if err := Extract(p, idx, store); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == string(diagnostics.CodeIMSDKArgumentMismatch) {
			t.Fatalf("unexpected im_sdk_argument_mismatch diagnostic: %v", diagnostic)
		}
	}
}

func callExpressions(fn *ast.FuncDecl) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			out = append(out, call)
		}
		return true
	})
	return out
}
