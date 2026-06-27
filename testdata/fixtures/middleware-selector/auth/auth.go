package auth

type Auth struct{}

func (a *Auth) Middleware() {
	_ = "new"
}
