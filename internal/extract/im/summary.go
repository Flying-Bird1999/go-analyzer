// summary.go 实现摘要传播层：先为每个函数构造直接摘要（SDK 调用、协议内调用、
// 协议直接发送），再通过不动点迭代把 event/payload/wrapper/control 依赖沿本仓调用链
// 向上传播，最终产出 IMEventFact。
package im

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// maxSummaryIterations 限制不动点摘要传播的最大迭代轮数。
// 传播本身通过 summaryKey 去重已能收敛，这里只是对病态调用图的防御性上限，
// 并非功能性限制；健康项目在远低于该上限的几轮内即可稳定。
const maxSummaryIterations = 1000

// templateKind 枚举值模板的种类。值模板用于把表达式抽象成可传播、可替换的形式。
type templateKind string

const (
	templateUnknown   templateKind = "unknown"   // 无法静态确定的值
	templateLiteral   templateKind = "literal"   // 字符串字面量或可静态求值出的字符串
	templateParam     templateKind = "param"     // 引用当前函数的某个参数
	templateField     templateKind = "field"     // 对某 base 的字段访问
	templateConcat    templateKind = "concat"    // 字符串拼接
	templateString    templateKind = "string"    // 枚举 .String() 调用
	templateCallback  templateKind = "callback"  // 回调型参数（参数本身就是函数，event 取实参）
	templateComposite templateKind = "composite" // 组合字面量（如 BroadcastParams{...}）
)

// valueTemplate 是值模板的统一表示。kind 不同时使用不同字段。
// 它把表达式抽象成可沿调用链传播、并在调用点用实参替换参数的形式。
type valueTemplate struct {
	kind       templateKind              // 模板种类
	literal    string                    // templateLiteral 时的具体字符串
	param      int                       // templateParam/templateCallback 时的参数下标
	field      string                    // templateField 时的字段名
	base       *valueTemplate            // templateField/templateString 时的 base 模板
	left       *valueTemplate            // templateConcat 时的左操作数
	right      *valueTemplate            // templateConcat 时的右操作数
	fields     map[string]*valueTemplate // templateComposite 时的字段映射
	typeIDs    []facts.SymbolID          // 该值可能涉及的类型符号，作为 payload 依赖
	symbolDeps []facts.SymbolID          // 该值引用的 const/value 符号，作为 event/payload 依赖
	raw        string                    // 原始表达式文本，用于诊断和 ID 生成
}

// functionInfo 是某个函数的预处理信息，用于摘要构造和传播。
type functionInfo struct {
	id          facts.SymbolID           // 函数符号 ID
	file        *project.File            // 函数所在文件
	decl        *ast.FuncDecl            // 函数声明 AST
	params      map[string]int           // 参数名 -> 参数下标
	paramTypes  map[int][]facts.SymbolID // 参数下标 -> 该参数涉及的类型符号集合
	assignments map[string]ast.Expr      // 局部变量名 -> 唯一赋值表达式（多次赋值的变量被排除）
	calls       []*ast.CallExpr          // 函数体内的所有调用表达式
}

// functionSummary 是某次发送的抽象摘要，可沿调用链传播。
// 它记录该发送的 event/payload 模板、wrapper 链和控制依赖，
// 让上游调用者只需替换参数即可继承这次发送的影响。
type functionSummary struct {
	function    facts.SymbolID   // 产生该摘要的函数符号
	event       *valueTemplate   // event 值模板
	payload     *valueTemplate   // payload 值模板
	wrappers    []facts.SymbolID // 经过的 wrapper 函数链
	controls    []facts.SymbolID // 控制依赖符号（if/switch 条件中引用的符号）
	controlExpr []ast.Expr       // 控制依赖的原始条件表达式，用于生成 evidence span
	call        *ast.CallExpr    // 触发该摘要的调用表达式
	eventExpr   ast.Expr         // event 实参表达式（用于 evidence）
	payloadExpr ast.Expr         // payload 实参表达式（用于 evidence）
}

// reachability 记录某函数是否可达 scheme/endpoint 锚点（直接或经被调用者间接）。
type reachability struct {
	scheme   bool // 函数体内或其调用链可达 broadcast:// 协议 scheme
	endpoint bool // 函数体内或其调用链可达 /broadcast/send 端点
}

// summaryEngine 是摘要传播引擎。它在构造时完成协议发现、函数索引和可达性分析，
// 随后通过 extract 执行不动点传播并产出 IMEventFact。
type summaryEngine struct {
	project         *project.Project                     // 当前项目
	index           *astindex.Index                      // 声明符号索引
	eval            *evaluator                           // 静态求值器
	anchors         protocolAnchors                      // 协议锚点
	functions       map[facts.SymbolID]*functionInfo     // 项目内所有函数的预处理信息
	reach           map[facts.SymbolID]reachability      // 各函数的锚点可达性
	summaries       map[facts.SymbolID][]functionSummary // 各函数已收集的摘要
	summaryKeys     map[facts.SymbolID]map[string]bool   // 各函数已收集摘要的去重键
	protocolValid   bool                                 // 协议锚点是否成立（双锚点同时存在）
	maxIterations   int                                  // 不动点迭代上限
	iterationCapped bool                                 // 是否触达迭代上限
}

// newSummaryEngine 构造摘要引擎：建立求值器、发现协议锚点、索引函数、计算可达性。
func newSummaryEngine(p *project.Project, idx *astindex.Index) *summaryEngine {
	engine := &summaryEngine{
		project:       p,
		index:         idx,
		eval:          newEvaluator(p, idx),
		anchors:       discoverProtocolAnchors(p, idx),
		functions:     map[facts.SymbolID]*functionInfo{},
		reach:         map[facts.SymbolID]reachability{},
		summaries:     map[facts.SymbolID][]functionSummary{},
		summaryKeys:   map[facts.SymbolID]map[string]bool{},
		maxIterations: maxSummaryIterations,
	}
	engine.protocolValid = engine.anchors.Valid()
	engine.indexFunctions()
	engine.buildReachability()
	return engine
}

// extract 执行摘要传播并产出 IMEventFact。
//
// 流程分三步：
//  1. 为每个函数构造直接摘要（直接发送 IM 或调用协议可达的 wrapper），加入摘要集合。
//  2. 不动点迭代：把被调用者的摘要替换为调用者实参后加入调用者，直到没有新摘要。
//  3. 把所有摘要投影为 IMEventFact，去重并按 ID 排序。
func (e *summaryEngine) extract() []facts.IMEventFact {
	// 第一步：直接摘要。遍历顺序固定，保证结果确定性。
	for _, id := range sortedFunctionIDs(e.functions) {
		info := e.functions[id]
		for _, summary := range e.directSummaries(info) {
			e.addSummary(summary)
		}
	}

	// 第二步：不动点传播。changed 标记本轮是否新增摘要；新增即继续下一轮。
	changed := true
	for iteration := 0; changed; iteration++ {
		// 触达防御性上限时停止并标记，由 Extract 输出诊断。
		if e.maxIterations > 0 && iteration >= e.maxIterations {
			e.iterationCapped = true
			break
		}
		changed = false
		for _, id := range sortedFunctionIDs(e.functions) {
			info := e.functions[id]
			for _, call := range info.calls {
				callee, ok := e.resolveLocalCall(info.file, call)
				if !ok {
					continue
				}
				// 把被调用者的每条摘要用调用者的实参替换参数模板，
				// 同时把被调用者本身记入 wrapper 链，并补上调用点的控制依赖。
				for _, calleeSummary := range e.summaries[callee] {
					summary := functionSummary{
						function:    info.id,
						event:       e.substitute(info, calleeSummary.event, call.Args, true),
						payload:     e.substitute(info, calleeSummary.payload, call.Args, false),
						wrappers:    appendUniqueSymbols(calleeSummary.wrappers, callee),
						controls:    uniqueSortedSymbols(append(calleeSummary.controls, e.controlDependencies(info, call)...)),
						controlExpr: e.controlExpressions(info, call),
						call:        call,
						eventExpr:   argumentAt(call.Args, templatePrimaryParam(calleeSummary.event)),
						payloadExpr: argumentAt(call.Args, templatePrimaryParam(calleeSummary.payload)),
					}
					if e.addSummary(summary) {
						changed = true
					}
				}
			}
		}
	}

	// 第三步：投影为 IMEventFact 并去重。
	var out []facts.IMEventFact
	seen := map[string]bool{}
	for _, id := range sortedFunctionIDs(e.functions) {
		info := e.functions[id]
		for _, summary := range e.summaries[id] {
			fact := e.factForSummary(info, summary)
			if seen[fact.ID] {
				continue
			}
			seen[fact.ID] = true
			out = append(out, fact)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// indexFunctions 扫描项目所有函数声明，为每个函数建立 functionInfo，
// 包括参数下标、参数类型集合、局部变量赋值表和函数体调用列表。
func (e *summaryEngine) indexFunctions() {
	for _, pkg := range e.project.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				fn, ok := rawDecl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				info := &functionInfo{
					id:          functionSymbolID(file, fn),
					file:        file,
					decl:        fn,
					params:      map[string]int{},
					paramTypes:  map[int][]facts.SymbolID{},
					assignments: map[string]ast.Expr{},
				}
				info.indexParams(e.eval)
				info.indexBody()
				e.functions[info.id] = info
			}
		}
	}
}

// indexParams 索引函数参数：参数名映射到下标，并求值每个参数涉及的类型符号集合。
// 参数类型集合是 payload 依赖传播的关键：传进来的 payload 类型会成为 IM 事件依赖。
func (i *functionInfo) indexParams(eval *evaluator) {
	index := 0
	if i.decl.Type.Params == nil {
		return
	}
	for _, field := range i.decl.Type.Params.List {
		for _, name := range field.Names {
			i.params[name.Name] = index
			i.paramTypes[index] = eval.expressionTypeIDs(i.file, i.decl, name)
			index++
		}
	}
}

// indexBody 索引函数体：记录局部变量的唯一赋值、所有调用表达式。
// 多次赋值的变量不记入 assignments，避免在传播时取到错误的最后赋值。
func (i *functionInfo) indexBody() {
	assignmentCount := map[string]int{}
	ast.Inspect(i.decl.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			if len(value.Rhs) == 0 {
				return true
			}
			for index, rawLHS := range value.Lhs {
				name, ok := rawLHS.(*ast.Ident)
				if !ok {
					continue
				}
				valueIndex := index
				if valueIndex >= len(value.Rhs) {
					valueIndex = len(value.Rhs) - 1
				}
				assignmentCount[name.Name]++
				i.assignments[name.Name] = value.Rhs[valueIndex]
			}
		case *ast.DeclStmt:
			// 函数体内的短变量声明 var/const。
			decl, ok := value.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, rawSpec := range decl.Specs {
				spec, ok := rawSpec.(*ast.ValueSpec)
				if !ok || len(spec.Values) == 0 {
					continue
				}
				for index, name := range spec.Names {
					valueIndex := index
					if valueIndex >= len(spec.Values) {
						valueIndex = len(spec.Values) - 1
					}
					assignmentCount[name.Name]++
					i.assignments[name.Name] = spec.Values[valueIndex]
				}
			}
		case *ast.CallExpr:
			i.calls = append(i.calls, value)
		}
		return true
	})
	// 仅保留唯一赋值的变量：多次赋值的变量值在静态分析中不可确定。
	for name, count := range assignmentCount {
		if count != 1 {
			delete(i.assignments, name)
		}
	}
}

// buildReachability 计算每个函数对 scheme/endpoint 锚点的可达性。
// 一个函数自身引用锚点、或其调用链中的被调用者可达锚点，都视为可达。
// 通过不动点迭代把可达性沿调用图反向传播到所有上游函数。
func (e *summaryEngine) buildReachability() {
	schemeSet := symbolSliceSet(e.anchors.SchemeSymbols)
	endpointSet := symbolSliceSet(e.anchors.EndpointSymbols)
	// 初始：函数自身直接引用锚点即为可达。
	for id, info := range e.functions {
		e.reach[id] = reachability{
			scheme:   schemeSet[id] || e.functionReferencesAny(info, schemeSet),
			endpoint: endpointSet[id] || e.functionReferencesAny(info, endpointSet),
		}
	}
	// 传播：任一被调用者可达则调用者也可达，迭代到稳定。
	changed := true
	for changed {
		changed = false
		for id, info := range e.functions {
			current := e.reach[id]
			next := current
			for _, call := range info.calls {
				callee, ok := e.resolveLocalCall(info.file, call)
				if !ok {
					continue
				}
				reached := e.reach[callee]
				next.scheme = next.scheme || reached.scheme
				next.endpoint = next.endpoint || reached.endpoint
			}
			if next != current {
				e.reach[id] = next
				changed = true
			}
		}
	}
}

// functionReferencesAny 判断函数体是否引用了 targets 集合中的任意 var/const 符号。
// 同时识别本地 ident 与跨包 selector 引用。
func (e *summaryEngine) functionReferencesAny(info *functionInfo, targets map[facts.SymbolID]bool) bool {
	found := false
	ast.Inspect(info.decl.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			for _, kind := range []string{"const", "var"} {
				if targets[astindex.ValueSymbolID(kind, info.file.Package.Path, value.Name)] {
					found = true
					return false
				}
			}
		case *ast.SelectorExpr:
			pkg, ok := value.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath := info.file.Imports[pkg.Name]
			for _, kind := range []string{"const", "var"} {
				if targets[astindex.ValueSymbolID(kind, importPath, value.Sel.Name)] {
					found = true
					return false
				}
			}
		}
		return !found
	})
	return found
}

// directSummaries 构造函数 info 的直接摘要。
// 直接摘要来源有三类：
//  1. 命中公共 IM SDK 的调用（如 notifyim.SendIm）。
//  2. 协议成立时，调用 BroadcastParams 风格 wrapper（callee 同时可达 scheme 与 endpoint）。
//  3. 协议成立时，函数自身直接发送到 endpoint（如直接 Post(/broadcast/send, ...)）。
func (e *summaryEngine) directSummaries(info *functionInfo) []functionSummary {
	var out []functionSummary
	for _, call := range info.calls {
		// 来源 1：公共 SDK 调用，按 adapter 给出的 event/payload 位置直接取实参。
		if args, ok := matchSDKCall(info.file, call); ok {
			out = append(out, e.summaryFromCall(info, call, call.Args[args.EventArg], call.Args[args.PayloadArg], nil))
			continue
		}
		// 来源 2：协议 wrapper 调用。仅当协议锚点成立时才识别，避免在非 IM 项目误报。
		if e.protocolValid {
			if eventExpr, payloadExpr, ok := e.broadcastParamsCall(info, call); ok {
				callee, _ := e.resolveLocalCall(info.file, call)
				out = append(out, e.summaryFromCall(info, call, eventExpr, payloadExpr, []facts.SymbolID{callee}))
			}
		}
	}
	// 来源 3：函数自身直接发送。callee 作为 wrapper 链为空。
	if e.protocolValid {
		if summary, ok := e.directProtocolSummary(info); ok {
			out = append(out, summary)
		}
	}
	return out
}

// summaryFromCall 把一次调用的 event/payload 表达式构造为 functionSummary。
// wrappers 记录该发送经过的 wrapper 函数链；controls 来自调用点所在的 if/switch 条件。
func (e *summaryEngine) summaryFromCall(
	info *functionInfo,
	call *ast.CallExpr,
	eventExpr ast.Expr,
	payloadExpr ast.Expr,
	wrappers []facts.SymbolID,
) functionSummary {
	return functionSummary{
		function:    info.id,
		event:       e.templateFromExpr(info, eventExpr, true, map[string]bool{}),
		payload:     e.templateFromExpr(info, payloadExpr, false, map[string]bool{}),
		wrappers:    uniqueSortedSymbols(wrappers),
		controls:    e.controlDependencies(info, call),
		controlExpr: e.controlExpressions(info, call),
		call:        call,
		eventExpr:   eventExpr,
		payloadExpr: payloadExpr,
	}
}

// broadcastParamsCall 识别 SC2 风格的 wrapper 调用：
// wrapper(BroadcastParams{Event: e}, payload)，其中 wrapper 同时可达 scheme 与 endpoint。
// 成功时返回 Event 字段表达式和紧随其后的 payload 实参表达式。
func (e *summaryEngine) broadcastParamsCall(info *functionInfo, call *ast.CallExpr) (ast.Expr, ast.Expr, bool) {
	callee, ok := e.resolveLocalCall(info.file, call)
	if !ok {
		return nil, nil, false
	}
	reached := e.reach[callee]
	// callee 必须同时可达 scheme 和 endpoint，才视作协议 wrapper。
	if !reached.scheme || !reached.endpoint {
		return nil, nil, false
	}
	// 在实参中找 BroadcastParams{Event: ...}，event 表达式取 Event 字段值，
	// payload 表达式取紧随其后的下一个实参（约定 wrapper 第二参数为 body）。
	for index, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, rawElement := range lit.Elts {
			element, ok := rawElement.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := element.Key.(*ast.Ident)
			if !ok || key.Name != "Event" || index+1 >= len(call.Args) {
				continue
			}
			return element.Value, call.Args[index+1], true
		}
	}
	return nil, nil, false
}

// directProtocolSummary 识别函数自身直接发送 IM 的场景：函数体可达 endpoint，
// 且包含 data.Event(e) 调用和 Body 字段赋值。
// 用于支持 SC2 风格的 SendData.Event / Body 直接发送模式。
func (e *summaryEngine) directProtocolSummary(info *functionInfo) (functionSummary, bool) {
	if !e.reach[info.id].endpoint {
		return functionSummary{}, false
	}
	var eventExpr ast.Expr
	var eventCall *ast.CallExpr
	var payloadExpr ast.Expr
	ast.Inspect(info.decl.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			// 识别 data.Event(topic) 形式的调用，event 取唯一实参。
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Event" && len(value.Args) == 1 {
				eventExpr = value.Args[0]
				eventCall = value
			}
		case *ast.CompositeLit:
			// 识别 Body 字段，payload 取其赋值表达式。
			for _, rawElement := range value.Elts {
				element, ok := rawElement.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := element.Key.(*ast.Ident)
				if ok && key.Name == "Body" {
					payloadExpr = element.Value
				}
			}
		}
		return true
	})
	if eventExpr == nil || payloadExpr == nil {
		return functionSummary{}, false
	}
	return e.summaryFromCall(info, eventCall, eventExpr, payloadExpr, nil), true
}

// addSummary 把 summary 加入对应函数的摘要集合，并对重复摘要去重。
// 去重键由 event/payload/wrappers/controls 共同构成，确保只有真正不同的摘要才被保留。
// 返回是否为新加入的摘要（用于不动点迭代判断是否需要继续）。
func (e *summaryEngine) addSummary(summary functionSummary) bool {
	if summary.event == nil || summary.payload == nil {
		return false
	}
	key := templateKey(summary.event) + "|" + templateKey(summary.payload) + "|" +
		symbolListKey(summary.wrappers) + "|" + symbolListKey(summary.controls)
	if e.summaryKeys[summary.function] == nil {
		e.summaryKeys[summary.function] = map[string]bool{}
	}
	if e.summaryKeys[summary.function][key] {
		return false
	}
	e.summaryKeys[summary.function][key] = true
	e.summaries[summary.function] = append(e.summaries[summary.function], summary)
	// 同函数内按 event+payload 模板键排序，保证摘要集合顺序稳定。
	sort.Slice(e.summaries[summary.function], func(i, j int) bool {
		left := templateKey(e.summaries[summary.function][i].event) + "|" + templateKey(e.summaries[summary.function][i].payload)
		right := templateKey(e.summaries[summary.function][j].event) + "|" + templateKey(e.summaries[summary.function][j].payload)
		return left < right
	})
	return true
}

// templateFromExpr 把表达式抽象为值模板。
// event=true 时优先尝试静态求值为字面量（便于直接得到 event 字符串）。
// visiting 记录正在展开的本地变量名，防止自引用导致的无限递归。
// 这是 payload 依赖传播的核心：模板保留了参数引用、字段链、类型符号等信息，
// 使得后续 substitute 能在调用点用实参完成替换。
func (e *summaryEngine) templateFromExpr(info *functionInfo, expr ast.Expr, event bool, visiting map[string]bool) *valueTemplate {
	if expr == nil {
		return &valueTemplate{kind: templateUnknown}
	}
	raw := renderExpr(expr)
	// event 表达式优先尝试静态求值为字面量，避免对简单字符串做不必要的模板化。
	if event {
		if value, ok := e.eval.eventValue(info.file, expr); ok {
			return &valueTemplate{
				kind:       templateLiteral,
				literal:    value,
				symbolDeps: e.symbolDependencies(info.file, expr),
				raw:        raw,
			}
		}
	}
	switch value := expr.(type) {
	case *ast.BasicLit:
		// 字符串字面量直接转为 literal 模板。
		if value.Kind == token.STRING {
			literal, err := strconv.Unquote(value.Value)
			if err == nil {
				return &valueTemplate{kind: templateLiteral, literal: literal, raw: raw}
			}
		}
	case *ast.Ident:
		// 参数引用：记录参数下标及其类型符号，便于 substitute 时用实参替换。
		if index, ok := info.params[value.Name]; ok {
			return &valueTemplate{
				kind:       templateParam,
				param:      index,
				typeIDs:    append([]facts.SymbolID(nil), info.paramTypes[index]...),
				symbolDeps: e.symbolDependencies(info.file, expr),
				raw:        raw,
			}
		}
		// 唯一赋值的局部变量：展开为赋值表达式的模板（带防环）。
		if assigned := info.assignments[value.Name]; assigned != nil && !visiting[value.Name] {
			next := copyStringSet(visiting)
			next[value.Name] = true
			return e.templateFromExpr(info, assigned, event, next)
		}
	case *ast.SelectorExpr:
		// 字段访问：先建 base 模板，再附加字段名；类型来自表达式求值或 base 字段表。
		base := e.templateFromExpr(info, value.X, event, visiting)
		typeIDs := e.eval.expressionTypeIDs(info.file, info.decl, expr)
		if len(typeIDs) == 0 {
			typeIDs = e.fieldTypeIDs(base.typeIDs, value.Sel.Name)
		}
		return &valueTemplate{
			kind:       templateField,
			field:      value.Sel.Name,
			base:       base,
			typeIDs:    typeIDs,
			symbolDeps: e.symbolDependencies(info.file, expr),
			raw:        raw,
		}
	case *ast.BinaryExpr:
		// 字符串拼接：保留左右子模板，最终可在 substitute 时折叠为字面量。
		if value.Op == token.ADD {
			return &valueTemplate{
				kind:  templateConcat,
				left:  e.templateFromExpr(info, value.X, event, visiting),
				right: e.templateFromExpr(info, value.Y, event, visiting),
				raw:   raw,
			}
		}
	case *ast.CallExpr:
		// 回调型参数：参数本身是函数，event/payload 取决于运行时实参。
		if ident, ok := value.Fun.(*ast.Ident); ok {
			if index, isParam := info.params[ident.Name]; isParam {
				return &valueTemplate{kind: templateCallback, param: index, raw: raw}
			}
		}
		// 项目内函数调用：透传单实参模板，并把被调用者及其返回类型记入依赖。
		// 这样 payload producer 函数会被记入 payload 依赖，支持 converter 风格。
		if callee, ok := e.resolveLocalCall(info.file, value); ok {
			out := &valueTemplate{kind: templateUnknown, raw: raw}
			if len(value.Args) == 1 {
				out = cloneTemplate(e.templateFromExpr(info, value.Args[0], event, visiting))
				out.raw = raw
			}
			out.symbolDeps = appendUniqueSymbols(out.symbolDeps, callee)
			if resultType, ok := e.index.CallableReturnTypes[callee]; ok && resultType.TypeName != "" {
				resultID := astindex.TypeSymbolID(resultType.PackagePath, resultType.TypeName)
				out.typeIDs = appendUniqueSymbols(out.typeIDs, resultID)
			}
			return out
		}
		// 内置类型转换或单实参函数：透传唯一实参的模板。
		if _, ok := value.Fun.(*ast.Ident); ok && len(value.Args) == 1 {
			return e.templateFromExpr(info, value.Args[0], event, visiting)
		}
		// x.String()：保留 base 模板，event 求值时尝试折叠为字面量。
		if selector, ok := value.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "String" {
			return &valueTemplate{
				kind: templateString,
				base: e.templateFromExpr(info, selector.X, event, visiting),
				raw:  raw,
			}
		}
	case *ast.CompositeLit:
		// 组合字面量：逐字段建立模板，并把整体类型记入依赖。
		fields := map[string]*valueTemplate{}
		for _, rawElement := range value.Elts {
			element, ok := rawElement.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := element.Key.(*ast.Ident)
			if !ok {
				continue
			}
			fields[key.Name] = e.templateFromExpr(info, element.Value, event, visiting)
		}
		return &valueTemplate{
			kind:    templateComposite,
			fields:  fields,
			typeIDs: e.eval.expressionTypeIDs(info.file, info.decl, expr),
			raw:     raw,
		}
	case *ast.FuncLit:
		// 闭包：识别单返回值闭包，取其返回表达式作为模板。支持 closure wrapper 模式。
		for _, stmt := range value.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if ok && len(ret.Results) == 1 {
				return e.templateFromExpr(info, ret.Results[0], event, visiting)
			}
		}
	case *ast.ParenExpr:
		// 括号包裹：透传内部表达式。
		return e.templateFromExpr(info, value.X, event, visiting)
	case *ast.UnaryExpr:
		// 一元（如取地址）：透传内部表达式。
		return e.templateFromExpr(info, value.X, event, visiting)
	}
	// 兜底：无法模板化时记录类型和符号依赖，便于 payload 依赖传播。
	return &valueTemplate{
		kind:       templateUnknown,
		typeIDs:    e.eval.expressionTypeIDs(info.file, info.decl, expr),
		symbolDeps: e.symbolDependencies(info.file, expr),
		raw:        raw,
	}
}

// fieldTypeIDs 在父类型集合中查找名为 fieldName 的字段，返回字段去重排序后的类型符号集合。
// 用于 selector 表达式在表达式求值失败时回退到结构体字段表查找 payload 类型。
func (e *summaryEngine) fieldTypeIDs(parents []facts.SymbolID, fieldName string) []facts.SymbolID {
	found := map[facts.SymbolID]struct{}{}
	for _, parent := range parents {
		field, ok := e.index.StructFieldTypes[parent][fieldName]
		if !ok || field.TypeName == "" {
			continue
		}
		found[astindex.TypeSymbolID(field.PackagePath, field.TypeName)] = struct{}{}
	}
	return sortedSymbolSet(found)
}

// substitute 把被调用者摘要模板中的参数引用替换为调用者的实参表达式模板。
// 这是传播的核心：被调用者的摘要通过实参替换"提升"到调用者，保留 wrapper 链和控制依赖。
// event 控制替换时是否尝试 event 风格的静态求值。
func (e *summaryEngine) substitute(info *functionInfo, template *valueTemplate, args []ast.Expr, event bool) *valueTemplate {
	if template == nil {
		return nil
	}
	switch template.kind {
	case templateParam:
		// 参数模板：用对应实参重新构造模板。
		if template.param >= 0 && template.param < len(args) {
			return e.templateFromExpr(info, args[template.param], event, map[string]bool{})
		}
	case templateCallback:
		// 回调型参数：用对应实参构造 payload 模板（回调返回的是 payload）。
		if template.param >= 0 && template.param < len(args) {
			return e.templateFromExpr(info, args[template.param], false, map[string]bool{})
		}
	case templateField:
		// 字段访问：递归替换 base。若 base 替换后是组合字面量，直接取其字段值，
		// 实现 BroadcastParams{Event: e}.Event 这类访问的折叠。
		base := e.substitute(info, template.base, args, event)
		if base != nil && base.kind == templateComposite {
			if field := base.fields[template.field]; field != nil {
				return field
			}
		}
		out := cloneTemplate(template)
		out.base = base
		return out
	case templateConcat:
		// 拼接：分别替换左右，若两边都能折叠为字面量则合并为 literal 模板。
		left := e.substitute(info, template.left, args, event)
		right := e.substitute(info, template.right, args, event)
		out := &valueTemplate{kind: templateConcat, left: left, right: right, raw: template.raw}
		if value, ok := concreteTemplateValue(out); ok {
			out.kind = templateLiteral
			out.literal = value
			out.left = nil
			out.right = nil
		}
		return out
	case templateString:
		// 枚举 .String()：若 base 是参数且对应实参可静态求值为枚举字符串，直接折叠。
		if template.base != nil && template.base.kind == templateParam &&
			template.base.param >= 0 && template.base.param < len(args) {
			actual := args[template.base.param]
			if value, ok := e.eval.enumStringValue(info.file, actual); ok {
				return &valueTemplate{
					kind:       templateLiteral,
					literal:    value,
					symbolDeps: e.symbolDependencies(info.file, actual),
					raw:        renderExpr(actual) + ".String()",
				}
			}
		}
		// 否则递归替换 base，若能折叠为字面量则合并。
		base := e.substitute(info, template.base, args, event)
		if value, ok := concreteTemplateValue(base); ok {
			return &valueTemplate{kind: templateLiteral, literal: value, raw: template.raw}
		}
		out := cloneTemplate(template)
		out.base = base
		return out
	case templateComposite:
		// 组合字面量：逐字段替换。
		out := cloneTemplate(template)
		out.fields = map[string]*valueTemplate{}
		for name, field := range template.fields {
			out.fields[name] = e.substitute(info, field, args, event)
		}
		return out
	}
	return cloneTemplate(template)
}

// factForSummary 把 functionSummary 投影为最终的 IMEventFact。
// 它聚合 payload/event/control 三类依赖，按 relation+symbol 去重并稳定排序，
// 同时收集调用点、event 实参、payload 实参和控制条件的 evidence span。
func (e *summaryEngine) factForSummary(info *functionInfo, summary functionSummary) facts.IMEventFact {
	event, resolved := concreteTemplateValue(summary.event)
	callSpan := spanForNode(e.project, info.file, summary.call)
	eventRaw := summary.event.raw
	if eventRaw == "" {
		eventRaw = event
	}
	dependencies := map[string]facts.IMEventDependency{}
	// addDependency 按 relation+symbol 去重写入依赖。
	addDependency := func(id facts.SymbolID, relation facts.IMEventRelation, confidence facts.Confidence) {
		if id == "" {
			return
		}
		key := string(relation) + "|" + string(id)
		dependencies[key] = facts.IMEventDependency{
			SymbolID:   id,
			Relation:   relation,
			Confidence: confidence,
		}
	}
	// payload 依赖：类型符号 + value 符号（如 converter 函数）。
	for _, id := range summary.payload.typeIDs {
		addDependency(id, facts.IMRelationPayload, facts.ConfidenceHigh)
	}
	for _, id := range summary.payload.symbolDeps {
		addDependency(id, facts.IMRelationPayload, facts.ConfidenceHigh)
	}
	// event 值依赖：event 表达式中引用的 const/value 符号。
	for _, id := range summary.event.symbolDeps {
		addDependency(id, facts.IMRelationEventValue, facts.ConfidenceHigh)
	}
	// 控制依赖：wrapper 链和 if/switch 条件中引用的符号。
	for _, id := range summary.wrappers {
		addDependency(id, facts.IMRelationControl, facts.ConfidenceHigh)
	}
	for _, id := range summary.controls {
		addDependency(id, facts.IMRelationControl, facts.ConfidenceHigh)
	}
	// 转为有序切片：先按 relation 再按 symbol ID。
	dependencyList := make([]facts.IMEventDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		dependencyList = append(dependencyList, dependency)
	}
	sort.Slice(dependencyList, func(i, j int) bool {
		if dependencyList[i].Relation != dependencyList[j].Relation {
			return dependencyList[i].Relation < dependencyList[j].Relation
		}
		return dependencyList[i].SymbolID < dependencyList[j].SymbolID
	})
	// evidence：调用点 span + event/payload 实参 span + 各控制条件 span。
	evidence := []facts.IMEventEvidence{{
		Relation: facts.IMRelationControl,
		Span:     callSpan,
	}}
	if summary.eventExpr != nil {
		evidence = append(evidence, facts.IMEventEvidence{
			Relation: facts.IMRelationEventValue,
			Span:     spanForNode(e.project, info.file, summary.eventExpr),
		})
	}
	if summary.payloadExpr != nil {
		evidence = append(evidence, facts.IMEventEvidence{
			Relation: facts.IMRelationPayload,
			Span:     spanForNode(e.project, info.file, summary.payloadExpr),
		})
	}
	for _, expr := range summary.controlExpr {
		evidence = append(evidence, facts.IMEventEvidence{
			Relation: facts.IMRelationControl,
			Span:     spanForNode(e.project, info.file, expr),
		})
	}
	return facts.IMEventFact{
		ID:           eventFactID(info.id, event, callSpan),
		Event:        event,
		EventRaw:     eventRaw,
		SenderSymbol: info.id,
		Dependencies: dependencyList,
		Evidence:     evidence,
		Confidence:   facts.ConfidenceHigh,
		Span:         callSpan,
		Resolved:     resolved,
	}
}

// controlDependencies 收集调用点 call 所在的 if/switch 条件中引用的符号。
// 这些符号构成该 IM 发送的控制依赖：只有这些符号（或其上游）发生变化时，
// 才可能改变该发送是否执行或发送什么 event。
func (e *summaryEngine) controlDependencies(info *functionInfo, call *ast.CallExpr) []facts.SymbolID {
	found := map[facts.SymbolID]struct{}{}
	for _, expr := range e.controlExpressions(info, call) {
		// 条件中引用的 const/value 符号。
		for _, id := range e.symbolDependencies(info.file, expr) {
			found[id] = struct{}{}
		}
		// 条件中的项目内函数调用也作为控制依赖。
		ast.Inspect(expr, func(node ast.Node) bool {
			candidate, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := e.resolveLocalCall(info.file, candidate); ok {
				found[id] = struct{}{}
			}
			return true
		})
	}
	return sortedSymbolSet(found)
}

// controlExpressions 找出包裹调用点 call 的 if/switch/type-switch 的条件表达式。
// 只有当 call 的位置落在某个分支体内时，才认为该分支的条件控制了这次调用。
func (e *summaryEngine) controlExpressions(info *functionInfo, call *ast.CallExpr) []ast.Expr {
	var out []ast.Expr
	ast.Inspect(info.decl.Body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.IfStmt:
			if spanContainsNode(stmt.Body, call) || spanContainsNode(stmt.Else, call) {
				out = append(out, stmt.Cond)
			}
		case *ast.SwitchStmt:
			if spanContainsNode(stmt.Body, call) && stmt.Tag != nil {
				out = append(out, stmt.Tag)
			}
		case *ast.TypeSwitchStmt:
			if spanContainsNode(stmt.Body, call) && stmt.Assign != nil {
				switch assign := stmt.Assign.(type) {
				case *ast.AssignStmt:
					out = append(out, assign.Rhs...)
				case *ast.ExprStmt:
					out = append(out, assign.X)
				}
			}
		}
		return true
	})
	return out
}

// spanContainsNode 判断 target 节点是否完全位于 container 的位置区间内。
// 用于判断某个调用是否落在 if/switch 分支体内。
func spanContainsNode(container ast.Node, target ast.Node) bool {
	return container != nil && target != nil && container.Pos() <= target.Pos() && target.End() <= container.End()
}

// resolveLocalCall 把调用表达式解析为项目内的函数符号 ID。
// 仅识别本地函数调用和 pkg.Func 形式的跨包函数调用（其中 pkg 为 import alias）。
// 解析失败或目标不在项目内时返回 false。
func (e *summaryEngine) resolveLocalCall(file *project.File, call *ast.CallExpr) (facts.SymbolID, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// 本地函数：当前包路径 + 函数名。
		id := astindex.FunctionSymbolID(file.Package.Path, fun.Name)
		_, ok := e.functions[id]
		return id, ok
	case *ast.SelectorExpr:
		// 跨包函数：通过 import alias 解析真实 import path。
		pkg, ok := fun.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return "", false
		}
		id := astindex.FunctionSymbolID(importPath, fun.Sel.Name)
		_, ok = e.functions[id]
		return id, ok
	default:
		return "", false
	}
}

// symbolDependencies 收集表达式中引用的 const 符号（项目内已索引的）。
// 这些符号会成为 event 值依赖或 payload 依赖。
func (e *summaryEngine) symbolDependencies(file *project.File, expr ast.Expr) []facts.SymbolID {
	found := map[facts.SymbolID]struct{}{}
	ast.Inspect(expr, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			id := astindex.ValueSymbolID("const", file.Package.Path, value.Name)
			if _, ok := e.index.Symbols[id]; ok {
				found[id] = struct{}{}
			}
		case *ast.SelectorExpr:
			pkg, ok := value.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath := file.Imports[pkg.Name]
			id := astindex.ValueSymbolID("const", importPath, value.Sel.Name)
			if _, ok := e.index.Symbols[id]; ok {
				found[id] = struct{}{}
			}
			return false
		}
		return true
	})
	return sortedSymbolSet(found)
}

// concreteTemplateValue 尝试把模板折叠为具体字符串值。
// 支持 literal 模板和可两侧折叠的 concat 模板；无法折叠时返回 ok=false。
func concreteTemplateValue(template *valueTemplate) (string, bool) {
	if template == nil {
		return "", false
	}
	switch template.kind {
	case templateLiteral:
		return template.literal, true
	case templateConcat:
		left, leftOK := concreteTemplateValue(template.left)
		right, rightOK := concreteTemplateValue(template.right)
		if leftOK && rightOK {
			return left + right, true
		}
	}
	return "", false
}

// templateKey 生成模板的去重键，用于 addSummary 判断摘要是否重复。
// 键包含种类、子结构、类型符号和 value 符号依赖，保证语义等价的模板键相同。
func templateKey(template *valueTemplate) string {
	if template == nil {
		return "<nil>"
	}
	var key string
	switch template.kind {
	case templateLiteral:
		key = "literal:" + template.literal
	case templateParam, templateCallback:
		key = string(template.kind) + ":" + strconv.Itoa(template.param)
	case templateField:
		key = "field:" + templateKey(template.base) + "." + template.field
	case templateConcat:
		key = "concat:" + templateKey(template.left) + "+" + templateKey(template.right)
	case templateString:
		key = "string:" + templateKey(template.base)
	case templateComposite:
		// 组合字面量：字段名排序后拼接，保证顺序无关。
		names := make([]string, 0, len(template.fields))
		for name := range template.fields {
			names = append(names, name)
		}
		sort.Strings(names)
		var parts []string
		for _, name := range names {
			parts = append(parts, name+"="+templateKey(template.fields[name]))
		}
		key = "composite:" + strings.Join(parts, ",")
	default:
		key = "unknown:" + template.raw
	}
	return key + "|types:" + symbolListKey(template.typeIDs) + "|deps:" + symbolListKey(template.symbolDeps)
}

// cloneTemplate 深拷贝模板的切片字段，避免共享底层数组导致后续修改互相影响。
func cloneTemplate(in *valueTemplate) *valueTemplate {
	if in == nil {
		return nil
	}
	out := *in
	out.typeIDs = append([]facts.SymbolID(nil), in.typeIDs...)
	out.symbolDeps = append([]facts.SymbolID(nil), in.symbolDeps...)
	return &out
}

// renderExpr 用 go/printer 把表达式渲染为文本，用于诊断和 eventRaw 字段。
// 渲染失败时返回空字符串（不影响主流程）。
func renderExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var out bytes.Buffer
	_ = printer.Fprint(&out, token.NewFileSet(), expr)
	return out.String()
}

// appendUniqueSymbols 把单个符号追加到切片并去重排序，返回新切片。
func appendUniqueSymbols(in []facts.SymbolID, value facts.SymbolID) []facts.SymbolID {
	out := append([]facts.SymbolID(nil), in...)
	if value != "" {
		out = append(out, value)
	}
	return uniqueSortedSymbols(out)
}

// uniqueSortedSymbols 把符号切片去重并升序排序。
func uniqueSortedSymbols(in []facts.SymbolID) []facts.SymbolID {
	set := map[facts.SymbolID]struct{}{}
	for _, value := range in {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedSymbolSet(set)
}

// symbolListKey 把符号切片转为逗号分隔的字符串键，用于去重键拼接。
func symbolListKey(in []facts.SymbolID) string {
	values := uniqueSortedSymbols(in)
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = string(value)
	}
	return strings.Join(parts, ",")
}

// sortedFunctionIDs 返回引擎中所有函数符号的升序切片，用于稳定遍历顺序。
func sortedFunctionIDs(in map[facts.SymbolID]*functionInfo) []facts.SymbolID {
	out := make([]facts.SymbolID, 0, len(in))
	for id := range in {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// symbolSliceSet 把符号切片转为集合 map，便于 O(1) 查询。
func symbolSliceSet(in []facts.SymbolID) map[facts.SymbolID]bool {
	out := make(map[facts.SymbolID]bool, len(in))
	for _, value := range in {
		out[value] = true
	}
	return out
}

// copyStringSet 复制字符串集合，递归展开局部变量时在新副本上记录防环标记。
func copyStringSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

// argumentAt 安全取 args[index]，越界时返回 nil。
func argumentAt(args []ast.Expr, index int) ast.Expr {
	if index < 0 || index >= len(args) {
		return nil
	}
	return args[index]
}

// templatePrimaryParam 返回模板中"主参数"的下标。
// 用于在传播时定位 event/payload 实参对应的调用点实参表达式（用于 evidence）。
// 递归穿透 field/string/concat 等复合结构，找到最左/最深处的参数引用。
func templatePrimaryParam(template *valueTemplate) int {
	if template == nil {
		return -1
	}
	switch template.kind {
	case templateParam, templateCallback:
		return template.param
	case templateField, templateString:
		return templatePrimaryParam(template.base)
	case templateConcat:
		// 拼接：优先取左子树的主参数，左侧没有再取右子树。
		if index := templatePrimaryParam(template.left); index >= 0 {
			return index
		}
		return templatePrimaryParam(template.right)
	default:
		return -1
	}
}
