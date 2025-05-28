package common

import (
	"bytes"
	"net/http"
)

const (
	MaxBodySize = 50_000 // 50 KB (uncompressed)
)

type ResponseWriter struct {
	http.ResponseWriter
	Body                   *bytes.Buffer
	IsSupportedContentType func(string) bool

	statusCode        int
	size              int64
	shouldCaptureBody *bool
	exceededMaxSize   bool
}

func (w *ResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *ResponseWriter) Write(b []byte) (int, error) {
	if w.shouldCaptureBody == nil {
		w.shouldCaptureBody = new(bool)
		*w.shouldCaptureBody = w.IsSupportedContentType(w.Header().Get("Content-Type"))
	}
	if *w.shouldCaptureBody && !w.exceededMaxSize {
		if w.Body.Len()+len(b) <= MaxBodySize {
			w.Body.Write(b)
		} else {
			w.Body.Reset()
			w.exceededMaxSize = true
		}
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += int64(n)
	return n, err
}

func (w *ResponseWriter) Status() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

func (w *ResponseWriter) Size() int64 {
	return w.size
}
