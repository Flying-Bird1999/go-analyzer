// template.go contains the value-template subsystem used by IM summary propagation.
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
)

// templateKind 枚举值模板的种类。值模板用于把表达式抽象成可传播、可替换的形式。
type templateKind string

const (
	templateUnknown     templateKind = "unknown"     // 无法静态确定的值
	templateLiteral     templateKind = "literal"     // 字符串字面量或可静态求值出的字符串
	templateParam       templateKind = "param"       // 引用当前函数的某个参数
	templateField       templateKind = "field"       // 对某 base 的字段访问
	templateConcat      templateKind = "concat"      // 字符串拼接
	templateString      templateKind = "string"      // 枚举 .String() 调用
	templateConditional templateKind = "conditional" // 按字符串条件选择分支
	templateCallback    templateKind = "callback"    // 回调型参数（参数本身就是函数，event 取实参）
	templateComposite   templateKind = "composite"   // 组合字面量（如 BroadcastParams{...}）
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
	condition  *valueTemplate            // templateConditional 时参与比较的值
	equals     string                    // templateConditional 的比较目标
	whenEqual  *valueTemplate            // templateConditional 的真分支
	whenOther  *valueTemplate            // templateConditional 的假分支
	fields     map[string]*valueTemplate // templateComposite 时的字段映射
	typeIDs    []facts.SymbolID          // 该值可能涉及的类型符号，作为 payload 依赖
	symbolDeps []facts.SymbolID          // 该值引用的 const/value 符号，作为 event/payload 依赖
	raw        string                    // 原始表达式文本，用于诊断和 ID 生成
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
	case templateConditional:
		out := cloneTemplate(template)
		out.condition = e.substitute(info, template.condition, args, event)
		out.whenEqual = e.substitute(info, template.whenEqual, args, event)
		out.whenOther = e.substitute(info, template.whenOther, args, event)
		if value, ok := concreteTemplateValue(out); ok {
			return &valueTemplate{kind: templateLiteral, literal: value, raw: template.raw}
		}
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
	case templateConditional:
		condition, conditionOK := concreteTemplateValue(template.condition)
		if !conditionOK {
			return "", false
		}
		if condition == template.equals {
			return concreteTemplateValue(template.whenEqual)
		}
		return concreteTemplateValue(template.whenOther)
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
	case templateConditional:
		key = "conditional:" + templateKey(template.condition) + "==" + template.equals + "?" + templateKey(template.whenEqual) + ":" + templateKey(template.whenOther)
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
	out.condition = cloneTemplate(in.condition)
	out.whenEqual = cloneTemplate(in.whenEqual)
	out.whenOther = cloneTemplate(in.whenOther)
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
	case templateConditional:
		if index := templatePrimaryParam(template.condition); index >= 0 {
			return index
		}
		if index := templatePrimaryParam(template.whenEqual); index >= 0 {
			return index
		}
		return templatePrimaryParam(template.whenOther)
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
