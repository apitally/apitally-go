package apitally

import (
	"github.com/gin-gonic/gin"

	"github.com/apitally/apitally-go/internal"
)

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
