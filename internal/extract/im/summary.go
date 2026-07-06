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

// maxSummaryIterations bounds the fixed-point summary propagation loop. The
// loop already converges via summaryKey de-duplication, so this is a defensive
// backstop against a pathological call graph, not a functional limit; a healthy
// project settles in a handful of passes far below this ceiling.
const maxSummaryIterations = 1000

type templateKind string

const (
	templateUnknown   templateKind = "unknown"
	templateLiteral   templateKind = "literal"
	templateParam     templateKind = "param"
	templateField     templateKind = "field"
	templateConcat    templateKind = "concat"
	templateString    templateKind = "string"
	templateCallback  templateKind = "callback"
	templateComposite templateKind = "composite"
)

type valueTemplate struct {
	kind       templateKind
	literal    string
	param      int
	field      string
	base       *valueTemplate
	left       *valueTemplate
	right      *valueTemplate
	fields     map[string]*valueTemplate
	typeIDs    []facts.SymbolID
	symbolDeps []facts.SymbolID
	raw        string
}

type functionInfo struct {
	id          facts.SymbolID
	file        *project.File
	decl        *ast.FuncDecl
	params      map[string]int
	paramTypes  map[int][]facts.SymbolID
	assignments map[string]ast.Expr
	calls       []*ast.CallExpr
}

type functionSummary struct {
	function    facts.SymbolID
	event       *valueTemplate
	payload     *valueTemplate
	wrappers    []facts.SymbolID
	controls    []facts.SymbolID
	controlExpr []ast.Expr
	call        *ast.CallExpr
	eventExpr   ast.Expr
	payloadExpr ast.Expr
}

type reachability struct {
	scheme   bool
	endpoint bool
}

type summaryEngine struct {
	project         *project.Project
	index           *astindex.Index
	eval            *evaluator
	anchors         protocolAnchors
	functions       map[facts.SymbolID]*functionInfo
	reach           map[facts.SymbolID]reachability
	summaries       map[facts.SymbolID][]functionSummary
	summaryKeys     map[facts.SymbolID]map[string]bool
	protocolValid   bool
	maxIterations   int
	iterationCapped bool
}

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

func (e *summaryEngine) extract() []facts.IMEventFact {
	for _, id := range sortedFunctionIDs(e.functions) {
		info := e.functions[id]
		for _, summary := range e.directSummaries(info) {
			e.addSummary(summary)
		}
	}

	changed := true
	for iteration := 0; changed; iteration++ {
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
	for name, count := range assignmentCount {
		if count != 1 {
			delete(i.assignments, name)
		}
	}
}

func (e *summaryEngine) buildReachability() {
	schemeSet := symbolSliceSet(e.anchors.SchemeSymbols)
	endpointSet := symbolSliceSet(e.anchors.EndpointSymbols)
	for id, info := range e.functions {
		e.reach[id] = reachability{
			scheme:   schemeSet[id] || e.functionReferencesAny(info, schemeSet),
			endpoint: endpointSet[id] || e.functionReferencesAny(info, endpointSet),
		}
	}
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

func (e *summaryEngine) directSummaries(info *functionInfo) []functionSummary {
	var out []functionSummary
	for _, call := range info.calls {
		if args, ok := matchSDKCall(info.file, call); ok {
			out = append(out, e.summaryFromCall(info, call, call.Args[args.EventArg], call.Args[args.PayloadArg], nil))
			continue
		}
		if e.protocolValid {
			if eventExpr, payloadExpr, ok := e.broadcastParamsCall(info, call); ok {
				callee, _ := e.resolveLocalCall(info.file, call)
				out = append(out, e.summaryFromCall(info, call, eventExpr, payloadExpr, []facts.SymbolID{callee}))
			}
		}
	}
	if e.protocolValid {
		if summary, ok := e.directProtocolSummary(info); ok {
			out = append(out, summary)
		}
	}
	return out
}

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

func (e *summaryEngine) broadcastParamsCall(info *functionInfo, call *ast.CallExpr) (ast.Expr, ast.Expr, bool) {
	callee, ok := e.resolveLocalCall(info.file, call)
	if !ok {
		return nil, nil, false
	}
	reached := e.reach[callee]
	if !reached.scheme || !reached.endpoint {
		return nil, nil, false
	}
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
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Event" && len(value.Args) == 1 {
				eventExpr = value.Args[0]
				eventCall = value
			}
		case *ast.CompositeLit:
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
	sort.Slice(e.summaries[summary.function], func(i, j int) bool {
		left := templateKey(e.summaries[summary.function][i].event) + "|" + templateKey(e.summaries[summary.function][i].payload)
		right := templateKey(e.summaries[summary.function][j].event) + "|" + templateKey(e.summaries[summary.function][j].payload)
		return left < right
	})
	return true
}

func (e *summaryEngine) templateFromExpr(info *functionInfo, expr ast.Expr, event bool, visiting map[string]bool) *valueTemplate {
	if expr == nil {
		return &valueTemplate{kind: templateUnknown}
	}
	raw := renderExpr(expr)
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
		if value.Kind == token.STRING {
			literal, err := strconv.Unquote(value.Value)
			if err == nil {
				return &valueTemplate{kind: templateLiteral, literal: literal, raw: raw}
			}
		}
	case *ast.Ident:
		if index, ok := info.params[value.Name]; ok {
			return &valueTemplate{
				kind:       templateParam,
				param:      index,
				typeIDs:    append([]facts.SymbolID(nil), info.paramTypes[index]...),
				symbolDeps: e.symbolDependencies(info.file, expr),
				raw:        raw,
			}
		}
		if assigned := info.assignments[value.Name]; assigned != nil && !visiting[value.Name] {
			next := copyStringSet(visiting)
			next[value.Name] = true
			return e.templateFromExpr(info, assigned, event, next)
		}
	case *ast.SelectorExpr:
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
		if value.Op == token.ADD {
			return &valueTemplate{
				kind:  templateConcat,
				left:  e.templateFromExpr(info, value.X, event, visiting),
				right: e.templateFromExpr(info, value.Y, event, visiting),
				raw:   raw,
			}
		}
	case *ast.CallExpr:
		if ident, ok := value.Fun.(*ast.Ident); ok {
			if index, isParam := info.params[ident.Name]; isParam {
				return &valueTemplate{kind: templateCallback, param: index, raw: raw}
			}
		}
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
		if _, ok := value.Fun.(*ast.Ident); ok && len(value.Args) == 1 {
			return e.templateFromExpr(info, value.Args[0], event, visiting)
		}
		if selector, ok := value.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "String" {
			return &valueTemplate{
				kind: templateString,
				base: e.templateFromExpr(info, selector.X, event, visiting),
				raw:  raw,
			}
		}
	case *ast.CompositeLit:
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
		for _, stmt := range value.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if ok && len(ret.Results) == 1 {
				return e.templateFromExpr(info, ret.Results[0], event, visiting)
			}
		}
	case *ast.ParenExpr:
		return e.templateFromExpr(info, value.X, event, visiting)
	case *ast.UnaryExpr:
		return e.templateFromExpr(info, value.X, event, visiting)
	}
	return &valueTemplate{
		kind:       templateUnknown,
		typeIDs:    e.eval.expressionTypeIDs(info.file, info.decl, expr),
		symbolDeps: e.symbolDependencies(info.file, expr),
		raw:        raw,
	}
}

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

func (e *summaryEngine) substitute(info *functionInfo, template *valueTemplate, args []ast.Expr, event bool) *valueTemplate {
	if template == nil {
		return nil
	}
	switch template.kind {
	case templateParam:
		if template.param >= 0 && template.param < len(args) {
			return e.templateFromExpr(info, args[template.param], event, map[string]bool{})
		}
	case templateCallback:
		if template.param >= 0 && template.param < len(args) {
			return e.templateFromExpr(info, args[template.param], false, map[string]bool{})
		}
	case templateField:
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
		base := e.substitute(info, template.base, args, event)
		if value, ok := concreteTemplateValue(base); ok {
			return &valueTemplate{kind: templateLiteral, literal: value, raw: template.raw}
		}
		out := cloneTemplate(template)
		out.base = base
		return out
	case templateComposite:
		out := cloneTemplate(template)
		out.fields = map[string]*valueTemplate{}
		for name, field := range template.fields {
			out.fields[name] = e.substitute(info, field, args, event)
		}
		return out
	}
	return cloneTemplate(template)
}

func (e *summaryEngine) factForSummary(info *functionInfo, summary functionSummary) facts.IMEventFact {
	event, resolved := concreteTemplateValue(summary.event)
	callSpan := spanForNode(e.project, info.file, summary.call)
	eventRaw := summary.event.raw
	if eventRaw == "" {
		eventRaw = event
	}
	dependencies := map[string]facts.IMEventDependency{}
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
	for _, id := range summary.payload.typeIDs {
		addDependency(id, facts.IMRelationPayload, facts.ConfidenceHigh)
	}
	for _, id := range summary.payload.symbolDeps {
		addDependency(id, facts.IMRelationPayload, facts.ConfidenceHigh)
	}
	for _, id := range summary.event.symbolDeps {
		addDependency(id, facts.IMRelationEventValue, facts.ConfidenceHigh)
	}
	for _, id := range summary.wrappers {
		addDependency(id, facts.IMRelationControl, facts.ConfidenceHigh)
	}
	for _, id := range summary.controls {
		addDependency(id, facts.IMRelationControl, facts.ConfidenceHigh)
	}
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

func (e *summaryEngine) controlDependencies(info *functionInfo, call *ast.CallExpr) []facts.SymbolID {
	found := map[facts.SymbolID]struct{}{}
	for _, expr := range e.controlExpressions(info, call) {
		for _, id := range e.symbolDependencies(info.file, expr) {
			found[id] = struct{}{}
		}
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

func spanContainsNode(container ast.Node, target ast.Node) bool {
	return container != nil && target != nil && container.Pos() <= target.Pos() && target.End() <= container.End()
}

func (e *summaryEngine) resolveLocalCall(file *project.File, call *ast.CallExpr) (facts.SymbolID, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		id := astindex.FunctionSymbolID(file.Package.Path, fun.Name)
		_, ok := e.functions[id]
		return id, ok
	case *ast.SelectorExpr:
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

func cloneTemplate(in *valueTemplate) *valueTemplate {
	if in == nil {
		return nil
	}
	out := *in
	out.typeIDs = append([]facts.SymbolID(nil), in.typeIDs...)
	out.symbolDeps = append([]facts.SymbolID(nil), in.symbolDeps...)
	return &out
}

func renderExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var out bytes.Buffer
	_ = printer.Fprint(&out, token.NewFileSet(), expr)
	return out.String()
}

func appendUniqueSymbols(in []facts.SymbolID, value facts.SymbolID) []facts.SymbolID {
	out := append([]facts.SymbolID(nil), in...)
	if value != "" {
		out = append(out, value)
	}
	return uniqueSortedSymbols(out)
}

func uniqueSortedSymbols(in []facts.SymbolID) []facts.SymbolID {
	set := map[facts.SymbolID]struct{}{}
	for _, value := range in {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedSymbolSet(set)
}

func symbolListKey(in []facts.SymbolID) string {
	values := uniqueSortedSymbols(in)
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = string(value)
	}
	return strings.Join(parts, ",")
}

func sortedFunctionIDs(in map[facts.SymbolID]*functionInfo) []facts.SymbolID {
	out := make([]facts.SymbolID, 0, len(in))
	for id := range in {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func symbolSliceSet(in []facts.SymbolID) map[facts.SymbolID]bool {
	out := make(map[facts.SymbolID]bool, len(in))
	for _, value := range in {
		out[value] = true
	}
	return out
}

func copyStringSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func argumentAt(args []ast.Expr, index int) ast.Expr {
	if index < 0 || index >= len(args) {
		return nil
	}
	return args[index]
}

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
		if index := templatePrimaryParam(template.left); index >= 0 {
			return index
		}
		return templatePrimaryParam(template.right)
	default:
		return -1
	}
}
