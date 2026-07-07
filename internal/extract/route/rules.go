// rules.go 实现 route 提取所需的命名规则：HTTP 方法识别、handler 包装器识别、
// 路由组包装器与路由组工厂的命名启发式。
package route

import "strings"

// httpMethods 列举 lego 支持的 HTTP 方法名（全大写），用于识别 g.GET / g.POST 等路由调用。
var httpMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {}, "HEAD": {}, "OPTIONS": {},
}

// handlerWrappers 列举项目内已知的 handler 包装器短名（小写），
// 命中时该调用被视为 handler 包装器，取其最后一个参数为被包裹的 handler。
var handlerWrappers = map[string]struct{}{
	"controllerwithreqresp":    {},
	"appcontrollerwithreqresp": {},
	"controllerwithresp":       {},
	"controller":               {},
	"middlewarecontroller":     {},
}

// isHTTPMethod 判断选择器名是否为受支持的 HTTP 方法。
func isHTTPMethod(name string) bool {
	_, ok := httpMethods[name]
	return ok
}

// isHandlerWrapper 判断调用短名是否属于已知的 handler 包装器。
func isHandlerWrapper(name string) bool {
	_, ok := handlerWrappers[strings.ToLower(name)]
	return ok
}

// isRouteGroupWrapper 判断调用短名是否像路由组包装器：
// 以 "add" 开头，或包含 "guard" / "validator" 子串。
func isRouteGroupWrapper(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "add") ||
		strings.Contains(name, "guard") ||
		strings.Contains(name, "validator")
}

// isRouteGroupFactory 判断调用短名是否像路由组工厂函数：
// 名字包含 "group" 且以 create/new/build 开头。
func isRouteGroupFactory(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "group") &&
		(strings.HasPrefix(name, "create") ||
			strings.HasPrefix(name, "new") ||
			strings.HasPrefix(name, "build"))
}
