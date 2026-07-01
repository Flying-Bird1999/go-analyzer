package im

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

const (
	imBroadcastScheme   = "broadcast://"
	imBroadcastEndpoint = "/broadcast/send"
)

type protocolAnchors struct {
	SchemeSymbols   []facts.SymbolID
	EndpointSymbols []facts.SymbolID
}

func (a protocolAnchors) Valid() bool {
	return len(a.SchemeSymbols) > 0 && len(a.EndpointSymbols) > 0
}

func discoverProtocolAnchors(p *project.Project, _ *astindex.Index) protocolAnchors {
	schemes := map[facts.SymbolID]struct{}{}
	endpoints := map[facts.SymbolID]struct{}{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				switch decl := rawDecl.(type) {
				case *ast.FuncDecl:
					id := functionSymbolID(file, decl)
					scheme, endpoint := protocolLiterals(decl.Body)
					if scheme {
						schemes[id] = struct{}{}
					}
					if endpoint {
						endpoints[id] = struct{}{}
					}
				case *ast.GenDecl:
					kind := valueKind(decl.Tok)
					if kind == "" {
						continue
					}
					for _, rawSpec := range decl.Specs {
						spec, ok := rawSpec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range spec.Names {
							if len(spec.Values) == 0 {
								continue
							}
							valueIndex := i
							if valueIndex >= len(spec.Values) {
								valueIndex = len(spec.Values) - 1
							}
							scheme, endpoint := protocolLiterals(spec.Values[valueIndex])
							id := astindex.ValueSymbolID(kind, file.Package.Path, name.Name)
							if scheme {
								schemes[id] = struct{}{}
							}
							if endpoint {
								endpoints[id] = struct{}{}
							}
						}
					}
				}
			}
		}
	}
	return protocolAnchors{
		SchemeSymbols:   sortedSymbolSet(schemes),
		EndpointSymbols: sortedSymbolSet(endpoints),
	}
}

func protocolLiterals(node ast.Node) (bool, bool) {
	var scheme bool
	var endpoint bool
	ast.Inspect(node, func(current ast.Node) bool {
		lit, ok := current.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		switch value {
		case imBroadcastScheme:
			scheme = true
		case imBroadcastEndpoint:
			endpoint = true
		}
		return true
	})
	return scheme, endpoint
}

func functionSymbolID(file *project.File, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(file.Package.Path, fn.Name.Name)
	}
	return astindex.MethodSymbolID(file.Package.Path, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func valueKind(tok token.Token) string {
	switch tok {
	case token.CONST:
		return "const"
	case token.VAR:
		return "var"
	default:
		return ""
	}
}

func sortedSymbolSet(values map[facts.SymbolID]struct{}) []facts.SymbolID {
	out := make([]facts.SymbolID, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
