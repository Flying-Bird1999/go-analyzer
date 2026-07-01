package im

import (
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

const commonIMSDKPath = "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

type sinkArguments struct {
	EventArg   int
	PayloadArg int
}

var commonIMSDKFunctions = map[string]sinkArguments{
	"SendIm":           {EventArg: 3, PayloadArg: 4},
	"SendImAsync":      {EventArg: 3, PayloadArg: 4},
	"SendImToUid":      {EventArg: 3, PayloadArg: 4},
	"SendImToUidAsync": {EventArg: 3, PayloadArg: 4},
}

func matchSDKCall(file *project.File, call *ast.CallExpr) (sinkArguments, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return sinkArguments{}, false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || file.Imports[pkg.Name] != commonIMSDKPath {
		return sinkArguments{}, false
	}
	args, ok := commonIMSDKFunctions[selector.Sel.Name]
	if !ok || args.EventArg >= len(call.Args) || args.PayloadArg >= len(call.Args) {
		return sinkArguments{}, false
	}
	return args, true
}
