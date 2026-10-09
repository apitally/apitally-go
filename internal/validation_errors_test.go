package internal

import (
	"context"
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

func TestValidationErrorsAreFilteredByStatus(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	validationErr := fmt.Errorf("binding: %w", fieldErrors{{"Item.Name", "required"}, {"Item.Price", "gt"}})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {
		requestStateFromContext(r.Context()).CaptureReturnedError(validationErr)
		w.WriteHeader(http.StatusBadRequest)
	})
	mux.HandleFunc("POST /teapots", func(w http.ResponseWriter, r *http.Request) {
		requestStateFromContext(r.Context()).CaptureReturnedError(validationErr)
		w.WriteHeader(http.StatusTeapot)
	})
	appURL := startTestApp(t, mux)

	for _, path := range []string{"/items", "/items", "/teapots"} {
		testutils.Send(t, http.MethodPost, appURL+path, "")
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
	assert.ElementsMatch(t, []any{event("/items", "Name", "required", 2), event("/items", "Price", "gt", 2)}, bodies)
}
