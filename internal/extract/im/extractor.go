// extractor.go 实现 IM 事件提取的对外入口：构造摘要引擎、收集事件并按需暴露诊断。
//
// Package im 发现 Go BFF 项目中的出站 IM 事件，并沿本仓调用链把 payload、event 和
// control 依赖向上传播到具体的发送点。
//
// 整体分为四层：
//   - 协议发现（protocol.go）：当项目同时出现 broadcast:// 协议 scheme 与
//     /broadcast/send 端点两个锚点时，才把本仓调用链识别为 IM transport。
//   - SDK 适配（adapter.go）：对公共 IM SDK 使用精确 import path + 函数名 + 参数位置
//     匹配，不按相似函数名猜测。
//   - 静态求值（expr.go）：支持字符串 literal、typed const、字符串拼接、
//     iota + String() 字符串表等 event 取值；无法静态确定的表达式保留 unresolved。
//   - 摘要传播（summary.go）：通过不动点迭代把 event/payload/wrapper/control 依赖沿
//     本仓调用链向上传播，最终产出 IMEventFact。
//
// 设计动机见 ARCHITECTURE.md 第 5.10 节。该包只负责提取事实，不参与影响范围传播。
package im

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// Extract 是 IM 事件提取的对外入口，由 app pipeline 在 facts 构建阶段调用。
// 它构造摘要引擎、运行不动点传播、把事件写入 store 并按 ID 排序，
// 同时在迭代触顶或 SDK 参数不匹配时补充诊断。
func Extract(p *project.Project, idx *astindex.Index, store *facts.Store) error {
	engine := newSummaryEngine(p, idx)
	events := engine.extract()
	store.IMEvents = append(store.IMEvents, events...)
	// 事件按 ID 排序，保证输出确定性。
	sort.Slice(store.IMEvents, func(i, j int) bool {
		return store.IMEvents[i].ID < store.IMEvents[j].ID
	})
	// 摘要传播触顶说明调用图异常，结果可能不完整，输出诊断以便人工复核。
	if engine.iterationCapped {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:     diagnostics.CodeIMSummaryIterationCapped,
			Severity: diagnostics.SeverityWarning,
			Message:  "IM summary propagation hit the iteration ceiling; results may be incomplete",
		})
	}
	reportSDKArgumentMismatches(p, store)
	return nil
}

// reportSDKArgumentMismatches 扫描整个项目，发现那些按精确 import path 和函数名命中
// 公共 IM SDK、但实参数量不足 event/payload 期望位置的调用。
// 这种组合说明业务方使用的 SDK 签名与内置 adapter 已经漂移；adapter 只能精确匹配，
// 否则会按"不像 SDK"静默放过，从而漏掉真实的出站 IM 发送。这里改为输出诊断，
// 让漂移可见而不是变成隐式的漏报。
func reportSDKArgumentMismatches(p *project.Project, store *facts.Store) {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				fn, ok := rawDecl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					// 先按精确身份判断是否为公共 IM SDK 调用，不关心实参数量。
					name, args, ok := sdkCandidate(file, call)
					if !ok {
						return true
					}
					// 实参足以承载 event/payload 位置时属于正常调用，不报。
					if args.EventArg < len(call.Args) && args.PayloadArg < len(call.Args) {
						return true
					}
					span := spanForNode(p, file, call)
					diagnostics.AddFact(store, diagnostics.Diagnostic{
						Code:     diagnostics.CodeIMSDKArgumentMismatch,
						Severity: diagnostics.SeverityWarning,
						Message: fmt.Sprintf(
							"IM SDK call %q has %d arguments; adapter expects event at index %d and payload at index %d, so this send is not analyzed",
							name, len(call.Args), args.EventArg, args.PayloadArg,
						),
						Span: span,
					})
					return true
				})
			}
		}
	}
}

// spanForNode 把 AST 节点的位置区间转换为项目相对路径下的 SourceSpan。
// project.File.Path 在内存中是绝对路径，这里转换为项目根目录相对路径以保持稳定输出。
func spanForNode(p *project.Project, file *project.File, node ast.Node) facts.SourceSpan {
	if node == nil {
		return facts.SourceSpan{}
	}
	start := file.FileSet.Position(node.Pos())
	end := file.FileSet.Position(node.End())
	rel, err := filepath.Rel(p.Root, file.Path)
	if err != nil {
		rel = file.Path
	}
	return facts.SourceSpan{
		File:      filepath.ToSlash(rel),
		StartLine: start.Line,
		StartCol:  start.Column,
		EndLine:   end.Line,
		EndCol:    end.Column,
	}
}

// eventFactID 构造 IMEventFact 的稳定 ID。
// 由发送者符号、event 值、span 文件和起止行列拼接而成；event 为空时用 "unresolved"
// 占位，保证未解析事件也能被唯一标识和去重。
func eventFactID(sender facts.SymbolID, event string, span facts.SourceSpan) string {
	if event == "" {
		event = "unresolved"
	}
	return fmt.Sprintf("im_event:%s:%s:%s:%d:%d", sender, event, span.File, span.StartLine, span.StartCol)
}
