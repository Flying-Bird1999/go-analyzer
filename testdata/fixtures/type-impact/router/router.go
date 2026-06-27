package router

import "example.com/type-impact/controller"

type Group struct{}

func (g *Group) POST(path string, handler any) {}

func Init(g *Group) {
	g.POST("/orders", controller.API.Create)
}
