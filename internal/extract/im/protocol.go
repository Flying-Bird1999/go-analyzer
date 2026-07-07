// protocol.go 实现 IM 协议发现层：通过 broadcast:// 协议 scheme 和
// /broadcast/send 端点两个锚点判断本仓调用链是否构成 IM transport。
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

// IM 协议的两个锚点字面量。只有项目源码中同时出现这两个字符串字面量时，
// 才把本仓调用链识别为 IM transport。这种双锚点策略避免了对单个相似函数名的误判，
// 也避免要求业务方维护框架配置。
const (
	imBroadcastScheme   = "broadcast://"
	imBroadcastEndpoint = "/broadcast/send"
)

// protocolAnchors 收集同时引用了 scheme 或 endpoint 字面量的声明符号集合。
// Valid() 表示两者都存在，此时才认为项目实现了 IM 协议。
type protocolAnchors struct {
	SchemeSymbols   []facts.SymbolID // 包含 broadcast:// 字面量的声明符号
	EndpointSymbols []facts.SymbolID // 包含 /broadcast/send 字面量的声明符号
}

// Valid 返回协议是否成立：scheme 与 endpoint 锚点必须同时存在。
func (a protocolAnchors) Valid() bool {
	return len(a.SchemeSymbols) > 0 && len(a.EndpointSymbols) > 0
}

// discoverProtocolAnchors 遍历项目的函数声明与 var/const 声明，
// 收集所有直接出现 broadcast:// 或 /broadcast/send 字面量的符号。
// 第二个参数仅为与其它发现函数保持签名一致，当前未使用。
func discoverProtocolAnchors(p *project.Project, _ *astindex.Index) protocolAnchors {
	schemes := map[facts.SymbolID]struct{}{}
	endpoints := map[facts.SymbolID]struct{}{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				switch decl := rawDecl.(type) {
				case *ast.FuncDecl:
					// 函数体内部包含锚点字面量时，把该函数记入对应集合。
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
					// var/const 声明：每个名字对应一个 value symbol，
					// 取其初始化表达式判断是否包含锚点字面量。
					for _, rawSpec := range decl.Specs {
						spec, ok := rawSpec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range spec.Names {
							if len(spec.Values) == 0 {
								continue
							}
							// 多名字共享少量 RHS 时（如 var a, b = f()），
							// Go 规定按位置对应，名字多于 RHS 时复用最后一个 RHS。
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

// protocolLiterals 递归遍历 node，返回它是否包含 scheme 与 endpoint 两个字面量。
// 用于判断一段表达式是否同时引用了 IM 协议的两个锚点。
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

// functionSymbolID 根据函数声明生成对应的符号 ID。
// 无 receiver 时为 function symbol，有 receiver 时为 method symbol。
func functionSymbolID(file *project.File, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(file.Package.Path, fn.Name.Name)
	}
	return astindex.MethodSymbolID(file.Package.Path, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

// valueKind 把 go/token 的 CONST/VAR 映射为事实模型中使用的 "const"/"var" 字符串，
// 其它 token 返回空串表示不是本包关心的 value 声明。
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

// sortedSymbolSet 把符号集合转为升序切片，保证锚点集合输出稳定、可比较。
func sortedSymbolSet(values map[facts.SymbolID]struct{}) []facts.SymbolID {
	out := make([]facts.SymbolID, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
