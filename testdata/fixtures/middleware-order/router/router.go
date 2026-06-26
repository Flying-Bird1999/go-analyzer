package router

type RouterGroup struct{}

func (g *RouterGroup) GET(path string, handler any)                    {}
func (g *RouterGroup) Group(path string, handlers ...any) *RouterGroup { return g }
func (g *RouterGroup) Use(handler any)                                 {}

func InitRouter(g *RouterGroup) {
	g.GET("/a", h1)
	g.Use(Auth())
	g.GET("/b", h2)
	child := g.Group("/child", Audit())
	child.GET("/c", h1)
}

func h1()        {}
func h2()        {}
func Auth() any  { return nil }
func Audit() any { return nil }
