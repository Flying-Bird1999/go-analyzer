// extractor.go 实现从 controller 处理函数注释中提取 HTTP 注解，并写入 facts Store。
//
// Package annotation 负责识别 controller 处函数注释里的 HTTP 接口注解
// （例如 @Get / @Post / @Put / @Delete / @Patch / @Head / @Options）。
//
// 它从变更后的 Go BFF 项目源码出发，把每个处理函数注释中的注解解析成
// {HTTP 方法, 路径, 处理函数 symbol} 三元组，并附加精确到注释行的 span，
// 输出到共享的 facts.Store 中供后续 link/impact 阶段消费。
//
// 该包内置上述七种 HTTP 方法对应的注解语法，业务方接入时无需提供任何注解
// 语法配置；非 HTTP 方法前缀（如 @Refactor / @Search）会被忽略。
//
// 注解 span 精确到注释行而不是整个函数体：改注释行命中 annotation_changed，
// 改函数签名或函数体命中所属 function/method symbol，从而保证“diff 定位符号”
// 的核心语义不被 annotation 覆盖。
package annotation

import (
	"go/ast"
	"path/filepath"
	"sort"
	"strconv"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// Extract 遍历项目中所有 Go 文件的声明，提取处理函数注释上的 HTTP 注解，
// 写入 store.Annotations，并按 ID 稳定排序。
//
// 第二个参数 astindex.Index 用于其它 extractor 的符号解析，annotation 提取
// 本身只依赖 AST 注释，不读取该索引，因此这里以 _ 接收以保留统一签名。
func Extract(p *project.Project, _ *astindex.Index, store *facts.Store) error {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				// 只关注函数声明，类型/var/const 不承载 HTTP 注解。
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				// 计算处理函数对应的稳定 symbol ID，作为注解归属的 handler。
				handler := handlerSymbolID(pkg.Path, fn)
				if fn.Doc == nil {
					continue
				}
				// 同一函数可能存在多条注解，annotationIndex 用于稳定区分。
				annotationIndex := 0
				for _, comment := range fn.Doc.List {
					item, ok := parseLine(cleanComment(comment.Text))
					if !ok {
						continue
					}
					// span 精确到当前注释行，便于 diff 精确定位 annotation_changed。
					span := astindex.SourceSpanFor(file.FileSet, comment.Pos(), comment.End())
					if rel, err := filepath.Rel(p.Root, span.File); err == nil {
						span.File = filepath.ToSlash(rel)
					}
					store.Annotations = append(store.Annotations, facts.AnnotationFact{
						ID:            annotationID(handler, item.Method, item.Path, annotationIndex),
						Kind:          "annotation",
						Method:        item.Method,
						Path:          item.Path,
						Raw:           item.Raw,
						HandlerSymbol: handler,
						Span:          span,
					})
					annotationIndex++
				}
			}
		}
	}
	// 按 ID 稳定排序，保证 facts 输出确定性，降低 golden/consumer 抖动。
	sort.SliceStable(store.Annotations, func(i, j int) bool {
		return store.Annotations[i].ID < store.Annotations[j].ID
	})
	return nil
}

// handlerSymbolID 根据函数是否带 receiver 返回 function 或 method 形式的稳定 symbol ID。
func handlerSymbolID(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

// annotationID 拼装形如 annotation:<handler>:<method>:<path>:<index> 的稳定注解 ID。
// index 区分同一 handler 上的多条注解，避免重复路径互相覆盖。
func annotationID(handler facts.SymbolID, method, path string, index int) string {
	return "annotation:" + string(handler) + ":" + method + ":" + path + ":" + strconv.Itoa(index)
}
