// summary.go 实现摘要传播层：先为每个函数构造直接摘要（SDK 调用、协议内调用、
// 协议直接发送），再通过不动点迭代把 event/payload/wrapper/control 依赖沿本仓调用链
// 向上传播，最终产出 IMEventFact。
package im

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// maxSummaryIterations 限制不动点摘要传播的最大迭代轮数。
// 传播本身通过 summaryKey 去重已能收敛，这里只是对病态调用图的防御性上限，
// 并非功能性限制；健康项目在远低于该上限的几轮内即可稳定。
const maxSummaryIterations = 1000

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
// 且存在对“持有 Body 字段的同一变量”的 Event(...) 调用（SC2 风格 SendData）。
// 要求 Event 调用接收者与 Body 字面量绑定到同一变量，避免把函数内无关的 *.Event()
// 调用或无关的 Body 字段错误配对成 IM event（Body 是极常见字段名）。
func (e *summaryEngine) directProtocolSummary(info *functionInfo) (functionSummary, bool) {
	if !e.reach[info.id].endpoint {
		return functionSummary{}, false
	}
	// 第一遍：收集被赋值为带 Body 字段的复合字面量的变量名 -> payload 表达式。
	// 同名变量只记首次赋值，保证源序确定性。
	bodyPayload := map[string]ast.Expr{}
	ast.Inspect(info.decl.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			if i >= len(assign.Rhs) {
				continue
			}
			switch left := lhs.(type) {
			case *ast.Ident:
				lit := compositeLitOf(assign.Rhs[i])
				if lit == nil {
					continue
				}
				if expr, ok := bodyFieldOf(lit); ok {
					addBodyPayload(bodyPayload, left.Name, expr)
				}
			case *ast.SelectorExpr:
				if left.Sel.Name != "Body" {
					continue
				}
				recv, ok := left.X.(*ast.Ident)
				if !ok {
					continue
				}
				addBodyPayload(bodyPayload, recv.Name, assign.Rhs[i])
			}
		}
		return true
	})
	if len(bodyPayload) == 0 {
		return functionSummary{}, false
	}
	// 第二遍：找到对上述变量的 Event(arg) 调用，取源序首个作为结构化绑定结果。
	var eventExpr ast.Expr
	var eventCall *ast.CallExpr
	var payloadExpr ast.Expr
	ast.Inspect(info.decl.Body, func(node ast.Node) bool {
		if eventExpr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Event" || len(call.Args) != 1 {
			return true
		}
		recv, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		payload, ok := bodyPayload[recv.Name]
		if !ok {
			return true
		}
		eventExpr = call.Args[0]
		eventCall = call
		payloadExpr = payload
		return false
	})
	if eventExpr == nil || payloadExpr == nil {
		return functionSummary{}, false
	}
	return e.summaryFromCall(info, eventCall, eventExpr, payloadExpr, nil), true
}

func addBodyPayload(bodyPayload map[string]ast.Expr, name string, expr ast.Expr) {
	if name == "" || expr == nil {
		return
	}
	if _, exists := bodyPayload[name]; exists {
		return
	}
	bodyPayload[name] = expr
}

// compositeLitOf 解包可能的取址表达式（&T{...}），返回内部的复合字面量；否则返回 nil。
func compositeLitOf(expr ast.Expr) *ast.CompositeLit {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return compositeLitOf(x.X)
		}
	case *ast.CompositeLit:
		return x
	}
	return nil
}

// bodyFieldOf 在复合字面量中查找 Body 字段，返回其值表达式。
func bodyFieldOf(lit *ast.CompositeLit) (ast.Expr, bool) {
	for _, raw := range lit.Elts {
		element, ok := raw.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := element.Key.(*ast.Ident)
		if ok && key.Name == "Body" {
			return element.Value, true
		}
	}
	return nil, false
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
	// 不在此处排序：摘要集合的最终顺序由 extract 第三步投影后按 IMEventFact ID 排序保证，
	// 集合本身由 summaryKeys 去重（与插入顺序无关），逐次插入排序只会带来 O(K²logK) 冗余。
	return true
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
