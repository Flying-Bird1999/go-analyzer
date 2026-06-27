package controller

import "example.com/gomod-impact/service"

// @Get /api/checkIn
func CheckIn() string {
	return service.CheckIn("ok")
}
