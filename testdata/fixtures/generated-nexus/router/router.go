package router

import apis "example.com/generated-nexus/apis"

type RouterGroup struct{}

func InitRouter(g *RouterGroup) {
	apis.RegisterRouters(g)
}
