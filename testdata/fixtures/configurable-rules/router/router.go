package router

import common "example.com/configurable-rules/controller"

type Group struct{}

func (g Group) Group(path string) Group         { return g }
func (g Group) SEARCH(path string, handler any) {}

func TenantShield(g Group) Group { return g }

func CustomController(handler any) any { return handler }

func InitRouter(group Group) {
	api := group.Group("/api")
	TenantShield(api).SEARCH("/checkIn", CustomController(common.CheckIn))
}
