package router

import common "example.com/route-wrapper/controller"

type RouterGroup struct{}
type MiddlewareFunc func()

func (g *RouterGroup) GET(path string, handler any) {}

func ControllerWithResp(handler any) any { return handler }
func MiddlewareController(m []MiddlewareFunc, handler any) any {
	return handler
}
func Guard(group *RouterGroup) *RouterGroup { return group }

func InitRouter(g *RouterGroup) {
	g.GET("/wrapped", MiddlewareController([]MiddlewareFunc{Auth}, ControllerWithResp(common.CheckIn)))
	Guard(g).GET("/guarded", common.CheckIn)
}

func Auth() {}
