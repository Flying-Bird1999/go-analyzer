package annotation

import (
	"go/ast"
	"strings"
)

type ParsedAnnotation struct {
	Method string
	Path   string
	Raw    string
}

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

func parseLine(line string) (ParsedAnnotation, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "@") {
		return ParsedAnnotation{}, false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ParsedAnnotation{}, false
	}
	method := strings.ToUpper(strings.TrimPrefix(fields[0], "@"))
	if !isAnnotationMethod(method) {
		return ParsedAnnotation{}, false
	}
	path := fields[1]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return ParsedAnnotation{Method: method, Path: path, Raw: line}, true
}

func isAnnotationMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func cleanComment(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")
	return strings.TrimSpace(text)
}
