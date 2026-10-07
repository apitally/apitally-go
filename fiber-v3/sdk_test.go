package apitally_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitally "github.com/apitally/apitally-go/fiber-v3"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestCaptureErrorRecordsCallerStack(t *testing.T) {
	server := setUp(t)
	app := fiber.New()
	apitally.Init(app, nil)
	app.Get("/items", func(c fiber.Ctx) error {
		apitally.CaptureError(c, errors.New("failed"))
		return nil
	})

	send(t, app, http.MethodGet, "/items", nil)
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	assert.Regexp(t, `^github.com/apitally/apitally-go/fiber-v3_test.TestCaptureErrorRecordsCallerStack.func1\n\t\S+/fiber-v3/sdk_test.go:\d+\n`, testutils.Attributes(spans[0].Events[0].Attributes)["exception.stacktrace"])
}
