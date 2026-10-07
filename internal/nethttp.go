package internal

import (
	"net/http"
)

// NetHTTPMiddleware returns net/http middleware that observes each request.
// routePattern returns the request's route template after the handler chain
// returned, or "" when no route matched.
func NetHTTPMiddleware(routePattern func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Activate()
			state, ctx := BeginRequest(r.Context(), NetHTTPRequestInfo(r))
			r = r.WithContext(ctx)
			rw := &responseWriter{ResponseWriter: w}
			defer func() {
				p := recover()
				status := rw.status
				if status == 0 {
					// A panic before the response started is assumed to become a 500.
					status = http.StatusOK
					if p != nil {
						status = http.StatusInternalServerError
					}
				}
				state.FinishObservation(TransportResult{Route: routePattern(r), StatusCode: status, ClientAddress: HostFromAddress(r.RemoteAddr)})
				if p != nil {
					panic(p)
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}

// NetHTTPRequestInfo returns the request data of a net/http request.
func NetHTTPRequestInfo(r *http.Request) RequestInfo {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return RequestInfo{
		Method:        r.Method,
		Scheme:        scheme,
		Host:          r.Host,
		Path:          r.URL.Path,
		Query:         r.URL.RawQuery,
		Header:        r.Header,
		ContentLength: r.ContentLength,
	}
}

// responseWriter records the response status.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	// Informational responses precede the final status.
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}
