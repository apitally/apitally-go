package common

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
)

const (
	MaxBodySize = 50_000 // 50 KB (uncompressed)
)

type ResponseWriter struct {
	http.ResponseWriter
	Body                   *bytes.Buffer
	CaptureBody            bool
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
		*w.shouldCaptureBody = w.CaptureBody && w.IsSupportedContentType(w.Header().Get("Content-Type"))
	}
	if *w.shouldCaptureBody && w.Body != nil && !w.exceededMaxSize {
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

// The below methods ensure that optional interfaces (Flusher, Hijacker, Pusher) implemented by the
// underlying ResponseWriter are still accessible when wrapped, preventing middleware from breaking
// advanced HTTP features like WebSockets, Server-Sent Events, and HTTP/2 Server Push.

func (w *ResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("underlying writer does not implement http.Hijacker")
}

func (w *ResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
