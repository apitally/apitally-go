package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apitally/apitally-go/internal/testutils"
)

// fieldErrors has the method set of go-playground/validator's
// ValidationErrors, which the root module does not import.
type fieldErrors []testFieldError

func (e fieldErrors) Error() string { return "validation failed" }

type testFieldError struct{ namespace, tag string }

func (e testFieldError) Namespace() string { return e.namespace }
func (e testFieldError) Field() string     { return e.namespace }
func (e testFieldError) Tag() string       { return e.tag }
func (e testFieldError) Error() string     { return "field " + e.namespace + " failed on " + e.tag }

func TestValidationErrorsAreReportedFromErrorChannelAndExplicitCapture(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	validationErr := fmt.Errorf("binding: %w", fieldErrors{{"Item.Name", "required"}, {"Item.Price", "gt"}})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {
		RequestStateFromContext(r.Context()).CaptureError(validationErr)
		w.WriteHeader(http.StatusBadRequest)
	})
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		CaptureValidationError(r.Context(), errors.Join(errors.New("binding failed"), fieldErrors{{"Order.Email", "email"}}))
	})
	mux.HandleFunc("POST /teapots", func(w http.ResponseWriter, r *http.Request) {
		RequestStateFromContext(r.Context()).CaptureError(validationErr)
		w.WriteHeader(http.StatusTeapot)
	})
	appURL := startTestApp(t, mux)

	for _, path := range []string{"/items", "/items", "/orders", "/teapots"} {
		req, _ := http.NewRequest(http.MethodPost, appURL+path, nil)
		testutils.Do(t, http.DefaultClient.Do, req)
	}
	require.NoError(t, Shutdown(context.Background()))

	var bodies []any
	for _, record := range server.Events(t, validationErrorEventName) {
		bodies = append(bodies, testutils.Value(record.Body))
	}
	event := func(path, field, tag string, count int64) map[string]any {
		return map[string]any{
			"method": "POST", "path": path, "source": "", "field": field, "type": tag,
			"message": "field Item." + field + " failed on " + tag, "counts": []any{map[string]any{"count": count}},
		}
	}
	emailEvent := event("/orders", "Email", "email", 1)
	emailEvent["message"] = "field Order.Email failed on email"
	assert.ElementsMatch(t, []any{event("/items", "Name", "required", 2), event("/items", "Price", "gt", 2), emailEvent}, bodies)
}
