package rootpoc

import (
	"github.com/gin-gonic/gin"
)

func (a *Apitally) Gin() gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		info := RequestInfo{Method: r.Method, Scheme: "http", Path: r.URL.Path, Route: c.FullPath(), UserAgent: r.UserAgent()}
		ctx, h := a.Begin(r.Context(), info, headerGetter(r.Header.Get))
		c.Request = r.WithContext(ctx)
		defer func() {
			p := recover()
			status := c.Writer.Status()
			if p != nil && !c.Writer.Written() {
				status = 500
			}
			h.Finish(c.FullPath(), responseAttributes(status, c.Writer.Size(), c.FullPath(), c.Writer.Header().Get("Content-Type")))
			if p != nil {
				panic(p)
			}
		}()
		c.Next()
	}
}
