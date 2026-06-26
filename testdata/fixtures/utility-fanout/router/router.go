package router

import common "example.com/utility-fanout/controller"

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any) {}

func InitRouter(g *RouterGroup) {
	g.GET("/checkIn", common.CheckIn)
}
