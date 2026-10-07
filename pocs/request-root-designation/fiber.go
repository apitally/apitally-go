package rootpoc

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

type fiberCompletionKey struct{}

type fiberCompletion struct {
	h                  *Handle
	status, size       int
	route, contentType string
}

func (f *fiberCompletion) Close() error {
	f.h.Finish(f.route, responseAttributes(f.status, f.size, f.route, f.contentType))
	return nil
}

func (a *Apitally) Fiber() fiber.Handler {
	return func(c fiber.Ctx) error {
		info := RequestInfo{
			Method:    strings.Clone(c.Method()),
			Scheme:    strings.Clone(c.Scheme()),
			Path:      strings.Clone(c.Path()),
			UserAgent: strings.Clone(c.Get(fiber.HeaderUserAgent)),
		}
		ctx, h := a.Begin(c.Context(), info, headerGetter(func(k string) string { return strings.Clone(c.Get(k)) }))
		c.SetContext(ctx)
		done := &fiberCompletion{h: h, status: 500}
		c.RequestCtx().SetUserValue(fiberCompletionKey{}, done)
		if err := c.Next(); err != nil {
			if c.App().ErrorHandler(c, err) != nil {
				_ = c.SendStatus(500)
			}
		}
		resp := c.Response()
		done.status, done.route = resp.StatusCode(), c.Route().Path
		done.contentType = string(resp.Header.ContentType())
		if !resp.IsBodyStream() {
			done.size = len(resp.Body())
		}
		return nil
	}
}
