// adapter_test.go 验证公共 IM SDK 适配器的精确匹配与签名漂移诊断。
package im

import (
	"go/ast"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
)

// TestCommonSDKAdapterMatchesExactImportedSymbols 验证四个内置 SDK 函数在精确
// import path 下能按 event=3/payload=4 的位置正确匹配。
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

// TestCommonSDKAdapterRejectsSameNameFromAnotherPackage 验证来自非 SDK import path
// 的同名函数不会被误判为公共 SDK 调用。
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

// TestSDKArgumentMismatchEmitsDiagnostic 验证 SDK 函数实参不足以承载 event/payload
// 位置时不会静默漏报，而是输出 im_sdk_argument_mismatch 诊断。
func TestSDKArgumentMismatchEmitsDiagnostic(t *testing.T) {
	// 已知的 SDK 函数若实参过少、无法承载期望的 event/payload 位置，
	// 不能被静默放过，应作为 im_sdk_argument_mismatch 诊断暴露出来。
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

// TestValidSDKCallEmitsNoArgumentMismatch 验证参数齐全的正常 SDK 调用不会触发
// 签名漂移诊断。
func TestValidSDKCallEmitsNoArgumentMismatch(t *testing.T) {
	// 形参齐全的正常 SDK 调用不应触发签名漂移诊断。
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

// callExpressions 收集函数体内的所有调用表达式，供测试断言使用。
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
