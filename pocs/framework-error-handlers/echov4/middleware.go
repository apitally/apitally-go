package echov4

import (
	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/labstack/echo/v4"
)

// Middleware lets the tests compare swallowing and propagating handled errors.
func Middleware(returnOriginal bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			state := &observe.State{}
			c.SetRequest(c.Request().WithContext(observe.Put(c.Request().Context(), state)))
			defer state.Recover()
			// Outer recovery has not run when this deferred observation executes.
			defer func() { state.Status = c.Response().Status }()

			err := next(c)
			if err != nil {
				state.Record(err)
				c.Error(err)
			}
			if returnOriginal {
				return err
			}
			return nil
		}
	}
}
