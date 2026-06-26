package generated

import common "example.com/generated-nexus/controller"

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any) {}

func RegisterRouter(g *RouterGroup) {
	g.GET("/generated/checkIn", common.CheckIn)
}
