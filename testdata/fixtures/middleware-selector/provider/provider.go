package provider

import "example.com/middleware-selector/auth"

type Dependencies struct {
	Auth auth.Auth
}

var Default = Dependencies{}
