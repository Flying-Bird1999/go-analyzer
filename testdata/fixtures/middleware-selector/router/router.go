package router

import (
	"example.com/middleware-selector/controller"
	"example.com/middleware-selector/provider"
)

type RouterGroup struct{}

func (g *RouterGroup) Use(middleware any)           {}
func (g *RouterGroup) GET(path string, handler any) {}

func InitRouter(g *RouterGroup) {
	g.Use(provider.Default.Auth.Middleware)
	g.GET("/orders", controller.List)
}
