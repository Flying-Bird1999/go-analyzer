package router

import "example.com/deleted-route/controller"

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any)  {}
func (g *RouterGroup) POST(path string, handler any) {}

func Health() {}

func Init(g *RouterGroup) {
	g.GET("/health", Health)
	_ = controller.Create
}
