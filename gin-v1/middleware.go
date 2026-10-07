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
		// The observer runs before existing middleware, including recovery,
		// and panics are captured inside existing recovery. Use also rebuilds
		// the engine's 404 and 405 handler chains.
		engine.Handlers = append(gin.HandlersChain{observe}, engine.Handlers...)
		engine.Use(capturePanic)
	})
}

func observe(c *gin.Context) {
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

// capturePanic captures the original panic value and stack inside existing
// recovery, which then writes its response for the observer to observe.
func capturePanic(c *gin.Context) {
	defer func() {
		if p := recover(); p != nil {
			internal.RequestStateFromContext(c).CapturePanic(p)
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
