package controller

import "example.com/mini-bff/service"

const DefaultChannel = "online"

var Common = CommonController{}

type CommonController struct{}

func (c CommonController) CheckIn() {
	service.CheckIn()
}
