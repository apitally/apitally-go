package apitally_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitally "github.com/apitally/apitally-go/chi-v5"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestCaptureErrorRecordsCallerStack(t *testing.T) {
	server := setUp(t)
	r := chi.NewRouter()
	apitally.Init(r, nil)
	r.Get("/items", func(w http.ResponseWriter, r *http.Request) {
		apitally.CaptureError(r.Context(), errors.New("failed"))
	})
	appURL := testutils.Serve(t, r)

	testutils.Get(t, appURL+"/items")
	shutDown(t)

	spans := server.Spans(t)
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events, 1)
	assert.Regexp(t, `^github.com/apitally/apitally-go/chi-v5_test.TestCaptureErrorRecordsCallerStack.func1\n\t\S+/chi-v5/sdk_test.go:\d+\n`, testutils.Attributes(spans[0].Events[0].Attributes)["exception.stacktrace"])
}
