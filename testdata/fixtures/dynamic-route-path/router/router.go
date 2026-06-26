package router

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any) {}

func InitRouter(g *RouterGroup) {
	const suffix = "/dynamic"
	g.GET("/api"+suffix, h)
}

func h() {}
