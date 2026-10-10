// Package apitally integrates Apitally with Gin.
package apitally

import (
	"net/http"

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

// defaultErrorBodies are the bodies of Gin's default 404 and 405 responses.
var defaultErrorBodies = map[int]string{
	http.StatusNotFound:         "404 page not found",
	http.StatusMethodNotAllowed: "405 method not allowed",
}

func middleware(c *gin.Context) {
	// Gin runs the handler chain of an unmatched request with the 404 or 405
	// status already set, and writes its default response after the chain.
	unmatchedStatus := 0
	if c.FullPath() == "" {
		unmatchedStatus = c.Writer.Status()
	}
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
			o.State.CaptureReturnedError(c.Errors[0].Err)
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
	// Writing Gin's default response here lets Apitally observe it; Gin then
	// skips its own because the response is written.
	if body, ok := defaultErrorBodies[unmatchedStatus]; ok && !c.Writer.Written() && c.Writer.Status() == unmatchedStatus {
		c.Writer.Header()["Content-Type"] = []string{gin.MIMEPlain}
		_, _ = c.Writer.WriteString(body)
	}
}

func listRoutes(engine *gin.Engine) []internal.Route {
	var routes []internal.Route
	for _, route := range engine.Routes() {
		routes = append(routes, internal.Route{Method: route.Method, Path: route.Path})
	}
	return routes
}

// responseWriter is a gin.ResponseWriter whose write paths go through
// Apitally's observed writer. Other methods, including those of later Gin
// releases, are Gin's own.
type responseWriter struct {
	gin.ResponseWriter
	observed *internal.ResponseWriter
}

func (w *responseWriter) WriteHeader(code int) {
	w.observed.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	return w.observed.Write(b)
}

func (w *responseWriter) WriteString(s string) (int, error) {
	return w.observed.WriteString(s)
}

func (w *responseWriter) Flush() {
	w.observed.Flush()
}

// Unwrap supports http.ResponseController, which gin.ResponseWriter does not
// declare.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.observed
}
