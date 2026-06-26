package router

import uc "example.com/handler-method-var/controller/uc"

type RouterGroup struct{}

func (g *RouterGroup) POST(path string, handler any) {}

func InitRouter(g *RouterGroup) {
	g.POST("/x", uc.MerchantSettingApi.UpdateSubMerchantSettingByCode)
}
