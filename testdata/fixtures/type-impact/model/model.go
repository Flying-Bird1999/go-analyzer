package model

type Address struct {
	City string `json:"city"`
}

type CreateOrderRequest struct {
	Address Address `json:"address"`
}

type CreateOrderResponse struct {
	ID string `json:"id"`
}
