package controller

import "example.com/utility-fanout/service"

// @Get /api/bff-web/common/checkIn
func CheckIn() string {
	return service.CheckIn()
}
