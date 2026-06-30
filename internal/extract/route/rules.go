package route

import "strings"

var httpMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {}, "HEAD": {}, "OPTIONS": {},
}

var handlerWrappers = map[string]struct{}{
	"controllerwithreqresp":    {},
	"appcontrollerwithreqresp": {},
	"controllerwithresp":       {},
	"controller":               {},
	"middlewarecontroller":     {},
}

func isHTTPMethod(name string) bool {
	_, ok := httpMethods[name]
	return ok
}

func isHandlerWrapper(name string) bool {
	_, ok := handlerWrappers[strings.ToLower(name)]
	return ok
}

func isRouteGroupWrapper(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "add") ||
		strings.Contains(name, "guard") ||
		strings.Contains(name, "validator")
}

func isRouteGroupFactory(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "group") &&
		(strings.HasPrefix(name, "create") ||
			strings.HasPrefix(name, "new") ||
			strings.HasPrefix(name, "build"))
}
