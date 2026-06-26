module example.com/gomod-change

go 1.24

require (
	github.com/gin-gonic/gin v1.10.0
	gopkg.inshopline.com/commons/lego/core v1.4.4 // indirect
)

replace github.com/gin-gonic/gin => github.com/gin-gonic/gin v1.10.1
