package rootpoc

import (
	"net/http"
	"strings"
)

func (a *Apitally) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := RequestInfo{Method: r.Method, Scheme: "http", Path: r.URL.Path, UserAgent: r.UserAgent()}
		ctx, h := a.Begin(r.Context(), info, headerGetter(r.Header.Get))
		r = r.WithContext(ctx)
		rw := &responseRecorder{ResponseWriter: w}
		defer func() {
			p := recover()
			if rw.status == 0 {
				rw.status = 200
				if p != nil {
					rw.status = 500
				}
			}
			route := r.Pattern
			if _, path, ok := strings.Cut(route, " "); ok {
				route = path
			}
			h.Finish(route, responseAttributes(rw.status, rw.size, route, w.Header().Get("Content-Type")))
			if p != nil {
				panic(p)
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status, size int
}

func (w *responseRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}
