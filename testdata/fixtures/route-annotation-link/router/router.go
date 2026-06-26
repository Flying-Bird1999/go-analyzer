package router

import common "example.com/route-annotation-link/controller"

type RouterGroup struct{}

func (g *RouterGroup) POST(path string, handler any) {}

func InitRouter(g *RouterGroup) {
	g.POST("/checkIn", common.CheckIn)
}
