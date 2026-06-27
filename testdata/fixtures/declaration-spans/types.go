package declarationspans

type Request struct {
	Name string `json:"name"`
}

var DefaultRequest = Request{
	Name: "default",
}
