// parser.go 实现 HTTP 注解的纯文本解析：识别 @Get / @Post 等前缀，抽出方法与路径。
package annotation

import (
	"go/ast"
	"strings"
)

// ParsedAnnotation 表示从注释行解析出的一条 HTTP 注解。
type ParsedAnnotation struct {
	Method string // HTTP 方法（已大写），例如 GET / POST
	Path   string // 路径，已规整为以 "/" 开头
	Raw    string // 去除注释符号后的原始行文本，用于调试与 evidence
}

// ParseAPIAnnotations 从函数文档注释组中解析出全部 HTTP 注解。
// 该函数对外暴露给需要复用同一解析逻辑的调用方（如 link/impact 内部校验）。
func ParseAPIAnnotations(doc *ast.CommentGroup) []ParsedAnnotation {
	if doc == nil {
		return nil
	}
	var out []ParsedAnnotation
	for _, comment := range doc.List {
		line := cleanComment(comment.Text)
		annotation, ok := parseLine(line)
		if ok {
			out = append(out, annotation)
		}
	}
	return out
}

// parseLine 解析单条已去除注释符号的注释行。
// 仅识别以 "@" 开头、首字段为内置 HTTP 方法、且至少带一个路径字段的注解；
// 不满足条件的行（普通文档、@Refactor 等非 HTTP 注解）返回 ok=false。
func parseLine(line string) (ParsedAnnotation, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "@") {
		return ParsedAnnotation{}, false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ParsedAnnotation{}, false
	}
	// 取 "@" 后的方法名并大写，统一与内置 HTTP 方法集合比较。
	method := strings.ToUpper(strings.TrimPrefix(fields[0], "@"))
	if !isAnnotationMethod(method) {
		return ParsedAnnotation{}, false
	}
	path := fields[1]
	// 注解路径若不以 "/" 开头则补齐，保证后续拼接和 ID 一致。
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return ParsedAnnotation{Method: method, Path: path, Raw: line}, true
}

// isAnnotationMethod 判断方法名是否属于内置支持的七种 HTTP 方法。
func isAnnotationMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

// cleanComment 去除注释的包裹符号（// 或 /* */）并 trim 两端空白，
// 让后续解析无需关心注释风格差异。
func cleanComment(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")
	return strings.TrimSpace(text)
}
