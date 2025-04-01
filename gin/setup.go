package gin

import (
	"github.com/apitally/apitally-go/common"
	"github.com/apitally/apitally-go/internal"
	"github.com/gin-gonic/gin"
)

func UseApitally(r *gin.Engine, config *common.ApitallyConfig) {
	client, err := internal.NewApitallyClient(*config)
	if err != nil {
		panic(err)
	}
	r.Use(ApitallyMiddleware(client))
}
