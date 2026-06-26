package apis

import generated "example.com/generated-nexus/apis/generated"

type RouterGroup struct{}

func RegisterRouters(g *RouterGroup) {
	generated.RegisterRouter(g)
}
