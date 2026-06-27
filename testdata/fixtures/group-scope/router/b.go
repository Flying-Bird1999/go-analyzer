package router

import "example.com/group-scope/controller"

func InitB(g *Group) {
	group := g.Group("/b")
	_ = group
	group.GET("/two", controller.API.Two)
}
