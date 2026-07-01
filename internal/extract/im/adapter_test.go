package im

import (
	"go/ast"
	"testing"
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
