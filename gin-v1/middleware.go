// Package apitally integrates Apitally with Gin.
package apitally

import (
	"github.com/gin-gonic/gin"

	"github.com/apitally/apitally-go/internal"
)

var framework = internal.FrameworkInfo{
	Name:       "gin",
	ModulePath: "github.com/gin-gonic/gin",
	ScopeName:  "github.com/apitally/apitally-go/gin-v1",
}

// Init instruments the engine with Apitally. Register recovery middleware
// first, as gin.Default does, then call Init, then register other
// middleware, groups and routes. Routes registered before Init are not
// monitored. A nil cfg is equivalent to NewConfig(). Init copies cfg, so
// later changes to it have no effect. Calling Init again for the same engine
// does nothing.
func Init(engine *gin.Engine, cfg *Config) {
	internal.Register(cfg, framework, func() []internal.Route { return listRoutes(engine) })
	internal.InstallOnce(engine, func() {
		if len(engine.Routes()) > 0 {
			internal.LogLateInitError()
		}
		engine.Use(middleware)
	})
}

func middleware(c *gin.Context) {
	o := internal.BeginNetHTTP(c.Writer, c.Request)
	c.Request = o.Request
	if o.State != nil {
		// Context.Value of gin.Context resolves only string keys to its own values.
		c.Set(internal.RequestStateKey, o.State)
	}
	c.Writer = &responseWriter{ResponseWriter: c.Writer, observed: o.Writer}
	defer func() {
		p := recover()
		if len(c.Errors) > 0 {
			o.State.CaptureError(c.Errors[0].Err)
		}
		status := c.Writer.Status()
		if p != nil && !c.Writer.Written() {
			status = 0
		}
		o.Finish(c.FullPath(), c.ClientIP(), status, p)
		if p != nil {
			panic(p)
		}
	}()
	c.Next()
}

func listRoutes(engine *gin.Engine) []internal.Route {
	var routes []internal.Route
	for _, route := range engine.Routes() {
		routes = append(routes, internal.Route{Method: route.Method, Path: route.Path})
	}
	return routes
}
