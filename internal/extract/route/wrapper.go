package route

import (
	"go/ast"

	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func groupForExpr(groups map[string]groupContext, expr ast.Expr, cfg config.Config) (groupContext, []facts.WrapperFact, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		group, ok := groups[x.Name]
		return group, nil, ok
	case *ast.CallExpr:
		name := shortCallName(x)
		if len(x.Args) == 0 {
			return groupContext{}, nil, false
		}
		if !cfg.IsRouteGroupWrapper(name) {
			return groupContext{}, nil, false
		}
		group, wrappers, ok := groupForExpr(groups, x.Args[0], cfg)
		if !ok {
			return groupContext{}, nil, false
		}
		if name != "" {
			wrappers = append([]facts.WrapperFact{{Name: name, Raw: exprString(x)}}, wrappers...)
		}
		return group, wrappers, true
	default:
		return groupContext{}, nil, false
	}
}
