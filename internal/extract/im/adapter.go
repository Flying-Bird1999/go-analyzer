// adapter.go 实现 SDK 适配层：对公共 IM SDK 使用精确 import path、函数名和参数位置
// 匹配，不按相似函数名猜测。这样能避免把同名函数误判为 SDK 调用，同时保留对签名漂移
// 的诊断能力（见 extractor.go 的 reportSDKArgumentMismatches）。
package im

import (
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// commonIMSDKPath 是内置支持的公共 IM SDK import path。
// 仅当业务方从这个精确路径导入时，才视其调用为公共 SDK 调用。
const commonIMSDKPath = "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

// sinkArguments 描述某个 SDK 函数中 event 与 payload 参数的位置（实参下标）。
type sinkArguments struct {
	EventArg   int // event 字符串所在的实参下标
	PayloadArg int // payload 所在的实参下标
}

// commonIMSDKFunctions 列出内置支持的公共 IM SDK 函数及其参数布局。
// 当前覆盖 SendIm、SendImAsync、SendImToUid、SendImToUidAsync，
// 它们的 event 都在第 3 个实参、payload 都在第 4 个实参。
var commonIMSDKFunctions = map[string]sinkArguments{
	"SendIm":           {EventArg: 3, PayloadArg: 4},
	"SendImAsync":      {EventArg: 3, PayloadArg: 4},
	"SendImToUid":      {EventArg: 3, PayloadArg: 4},
	"SendImToUidAsync": {EventArg: 3, PayloadArg: 4},
}

// sdkCandidate 判断 call 是否调用内置公共 IM SDK 函数。
// 判断只看身份：精确 import path + 精确函数名，不关心实际传入的实参数量。
// 因此它既用于"匹配成功"，也用于诊断 SDK 签名漂移（实参不足的情况）。
// 返回值：函数名、参数布局、是否命中。
func sdkCandidate(file *project.File, call *ast.CallExpr) (string, sinkArguments, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", sinkArguments{}, false
	}
	pkg, ok := selector.X.(*ast.Ident)
	// 必须形如 pkg.Func，且 pkg 解析出的 import path 精确等于公共 SDK 路径。
	if !ok || file.Imports[pkg.Name] != commonIMSDKPath {
		return "", sinkArguments{}, false
	}
	args, ok := commonIMSDKFunctions[selector.Sel.Name]
	if !ok {
		return "", sinkArguments{}, false
	}
	return selector.Sel.Name, args, true
}

// matchSDKCall 在 sdkCandidate 身份匹配的基础上，额外校验实参数量足以承载
// event/payload 位置。仅当身份匹配且实参充足时才认为是一次可分析的 SDK 调用。
// 实参不足的情况由 reportSDKArgumentMismatches 单独诊断，不会在这里被静默放过。
func matchSDKCall(file *project.File, call *ast.CallExpr) (sinkArguments, bool) {
	_, args, ok := sdkCandidate(file, call)
	if !ok {
		return sinkArguments{}, false
	}
	if args.EventArg >= len(call.Args) || args.PayloadArg >= len(call.Args) {
		return sinkArguments{}, false
	}
	return args, true
}
