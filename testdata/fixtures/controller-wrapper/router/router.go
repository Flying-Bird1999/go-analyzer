package router

import common "example.com/controller-wrapper/controller"

type RouterGroup struct{}

func (g *RouterGroup) Group(path string, handlers ...any) *RouterGroup { return g }
func (g *RouterGroup) POST(path string, handler any)                   {}

func InitRouter(g *RouterGroup) {
	group := g.Group("/api/bff-web/common")
	group.POST("/checkIn", common.CheckIn)
}
