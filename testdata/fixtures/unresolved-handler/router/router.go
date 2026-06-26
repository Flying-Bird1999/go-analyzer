package router

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any) {}

func InitRouter(g *RouterGroup) {
	handlers := map[string]any{"x": h}
	g.GET("/x", handlers["x"])
}

func h() {}
