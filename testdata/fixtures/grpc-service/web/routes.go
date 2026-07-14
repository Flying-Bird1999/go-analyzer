package web

import "example.com/grpcservice/service"

type RouterGroup struct{}

func (g *RouterGroup) Group(_ string) *RouterGroup { return g }
func (g *RouterGroup) GET(_ string, _ any)         {}

type Handler struct{}

func (h *Handler) Reply() string { return service.BuildReply() }
func (h *Handler) Other() string { return "ok" }

type Router struct {
	Handler *Handler
}

func (r *Router) RegisterRoutes(root *RouterGroup) {
	group := root.Group("/internal")
	group.GET("/reply", r.Handler.Reply)
	group.GET("/other", r.Handler.Other)
}

func Start() { (&Router{Handler: &Handler{}}).RegisterRoutes(&RouterGroup{}) }
