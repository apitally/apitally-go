package echov5

import (
	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/labstack/echo/v5"
)

// Middleware lets the tests compare swallowing and propagating handled errors.
func Middleware(returnOriginal bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			state := &observe.State{}
			c.SetRequest(c.Request().WithContext(observe.Put(c.Request().Context(), state)))
			defer state.Recover()
			// Outer recovery has not run when this deferred observation executes.
			defer func() { _, state.Status = echo.ResolveResponseStatus(c.Response(), nil) }()

			err := next(c)
			if err != nil {
				state.Record(err)
				c.Echo().HTTPErrorHandler(c, err)
			}
			if returnOriginal {
				return err
			}
			return nil
		}
	}
}
