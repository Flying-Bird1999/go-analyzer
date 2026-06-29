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
	_, ok := httpMethods[strings.ToUpper(name)]
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
