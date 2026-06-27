package route

import (
	"go/ast"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type ParsedRouteCall struct {
	GroupRaw        string
	Method          string
	LocalPath       string
	PathRaw         string
	HandlerRaw      string
	GroupWrappers   []facts.WrapperFact
	HandlerWrappers []facts.WrapperFact
}

func ParseRouteCall(call *ast.CallExpr, cfg config.Config) (ParsedRouteCall, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !cfg.IsHTTPMethod(selector.Sel.Name) || len(call.Args) < 2 {
		return ParsedRouteCall{}, false
	}
	groupRaw, groupWrappers, ok := parseRouteGroupExpr(selector.X, cfg)
	if !ok {
		return ParsedRouteCall{}, false
	}
	localPath, ok := stringLiteral(call.Args[0])
	pathRaw := ""
	if !ok {
		pathRaw = exprString(call.Args[0])
	}
	handlerRaw, handlerWrappers := unwrapHandler(call.Args[1], cfg)
	return ParsedRouteCall{
		GroupRaw:        groupRaw,
		Method:          strings.ToUpper(selector.Sel.Name),
		LocalPath:       localPath,
		PathRaw:         pathRaw,
		HandlerRaw:      handlerRaw,
		GroupWrappers:   groupWrappers,
		HandlerWrappers: handlerWrappers,
	}, true
}

func parseRouteGroupExpr(expr ast.Expr, cfg config.Config) (string, []facts.WrapperFact, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name, nil, true
	case *ast.CallExpr:
		name := shortCallName(x)
		if len(x.Args) == 0 || !cfg.IsRouteGroupWrapper(name) {
			return "", nil, false
		}
		groupRaw, wrappers, ok := parseRouteGroupExpr(x.Args[0], cfg)
		if !ok {
			return "", nil, false
		}
		if name != "" {
			wrappers = append([]facts.WrapperFact{{Name: name, Raw: exprString(x)}}, wrappers...)
		}
		return groupRaw, wrappers, true
	default:
		return "", nil, false
	}
}
