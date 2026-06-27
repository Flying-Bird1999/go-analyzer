package router

import "example.com/group-scope/controller"

type Group struct{}

func (g *Group) Group(path string) *Group       { return g }
func (g *Group) Use(middleware any)             {}
func (g *Group) GET(path string, handler any)   {}
func (g *Group) POST(path string, handler any)  {}
func (g *Group) PATCH(path string, handler any) {}

func AuthA() any { return nil }

func InitA(g *Group) {
	group := g.Group("/a")
	group.Use(AuthA())
	group.GET("/one", controller.API.One)
}
