package controller

import "example.com/type-impact/model"

type OrderAPI struct{}

type OrderID string

var API = &OrderAPI{}
var DefaultRequest = model.CreateOrderRequest{}

const Timeout = 5

func Build() {
	_ = Timeout
	_ = DefaultRequest
}

// @Post /orders
func (api *OrderAPI) Create(req model.CreateOrderRequest) model.CreateOrderResponse {
	_ = OrderID("sample")
	return model.CreateOrderResponse{ID: req.Address.City}
}
