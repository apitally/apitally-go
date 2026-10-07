package internal

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
)

// NetHTTPMiddleware returns net/http middleware that observes each request.
// routePattern returns the request's route template after the handler chain
// returned, or "" when no route matched.
func NetHTTPMiddleware(routePattern func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o := BeginNetHTTP(w, r)
			defer func() {
				p := recover()
				o.Finish(routePattern(o.Request), HostFromAddress(o.Request.RemoteAddr), p)
				if p != nil {
					panic(p)
				}
			}()
			next.ServeHTTP(o.Writer, o.Request)
		})
	}
}

// NetHTTPObservation observes a net/http request and its response.
type NetHTTPObservation struct {
	State *RequestState
	// Request and Writer are passed to the handler chain.
	Request *http.Request
	Writer  *ResponseWriter
	body    *requestBody
}

// BeginNetHTTP activates Apitally, begins the request, wraps the response
// writer and, when the body is captured or has no declared length, the
// request body.
func BeginNetHTTP(w http.ResponseWriter, r *http.Request) *NetHTTPObservation {
	Activate()
	info := NetHTTPRequestInfo(r)
	state, ctx := BeginRequest(r.Context(), info)
	o := &NetHTTPObservation{State: state, Request: r.WithContext(ctx), Writer: &ResponseWriter{ResponseWriter: w, state: state}}
	if state != nil && r.Body != nil && r.Body != http.NoBody {
		isCaptured := state.shouldCaptureRequestBody()
		if isCaptured || r.ContentLength < 0 {
			o.body = &requestBody{ReadCloser: r.Body}
			if isCaptured {
				o.body.capture = newBodyCapture(r.ContentLength)
			}
			o.Request.Body = o.body
		}
	}
	return o
}

// Finish completes observation. recovered is the value of a panic unwinding
// the handler chain, or nil.
func (o *NetHTTPObservation) Finish(route, clientAddress string, recovered any) {
	o.State.CapturePanic(recovered)
	status := o.Writer.status
	if status == 0 {
		// A panic before the response started is assumed to become a 500.
		status = http.StatusOK
		if recovered != nil {
			status = http.StatusInternalServerError
		}
	}
	result := TransportResult{
		Route:            route,
		StatusCode:       status,
		ClientAddress:    clientAddress,
		RequestBodySize:  o.Request.ContentLength,
		ResponseBodySize: o.Writer.size,
		ResponseHeader:   o.Writer.Header(),
	}
	if b := o.body; b != nil {
		if result.RequestBodySize < 0 && b.isEOF {
			result.RequestBodySize = b.size
		}
		result.RequestBody = b.capture.body(b.isEOF || b.size == o.Request.ContentLength)
	}
	declared := declaredContentLength(result.ResponseHeader)
	result.ResponseBody = o.Writer.capture.body(recovered == nil && !o.Writer.hasWriteError && (declared < 0 || declared == o.Writer.size))
	o.State.FinishObservation(result)
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

// requestBody counts, and optionally captures, the bytes the application
// reads from a request body. It never reads more itself.
type requestBody struct {
	io.ReadCloser
	size    int64
	isEOF   bool
	capture *bodyCapture
}

func (b *requestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.size += int64(n)
	if b.capture != nil {
		b.capture.write(p[:n])
	}
	if err == io.EOF {
		b.isEOF = true
	}
	return n, err
}

// ResponseWriter records the response status, counts the body bytes written
// and captures the body. It forwards the optional interfaces of the wrapped
// writer.
type ResponseWriter struct {
	http.ResponseWriter
	state            *RequestState
	status           int
	size             int64
	capture          *bodyCapture
	isCaptureDecided bool
	hasWriteError    bool
}

func (w *ResponseWriter) WriteHeader(code int) {
	// Informational responses precede the final status.
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *ResponseWriter) Write(b []byte) (int, error) {
	w.startBody()
	n, err := w.ResponseWriter.Write(b)
	w.recordWrite(b[:n], err)
	return n, err
}

// ReadFrom keeps the wrapped writer's io.ReaderFrom, such as sendfile for
// files, while the body is not captured. A captured body is copied through
// Write, because io.Copy uses ReadFrom for any source, not only files.
func (w *ResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.startBody()
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok && w.capture == nil {
		n, err := readerFrom.ReadFrom(src)
		w.size += n
		w.hasWriteError = w.hasWriteError || err != nil
		return n, err
	}
	return io.Copy(writerOnly{w}, src)
}

func (w *ResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		w.startBody()
		flusher.Flush()
	}
}

func (w *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, errors.New("http.Hijacker is not supported by the wrapped response writer")
}

func (w *ResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

// Unwrap supports http.ResponseController.
func (w *ResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// startBody decides on body capture from the final response headers.
func (w *ResponseWriter) startBody() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.isCaptureDecided {
		w.isCaptureDecided = true
		if w.state.shouldCaptureResponseBody(w.Header()) {
			w.capture = newBodyCapture(declaredContentLength(w.Header()))
		}
	}
}

func (w *ResponseWriter) recordWrite(p []byte, err error) {
	w.size += int64(len(p))
	if w.capture != nil {
		w.capture.write(p)
	}
	w.hasWriteError = w.hasWriteError || err != nil
}

// writerOnly hides ResponseWriter.ReadFrom from io.Copy.
type writerOnly struct {
	io.Writer
}

// declaredContentLength returns -1 when the length is absent or the body is
// chunked.
func declaredContentLength(header http.Header) int64 {
	if header.Get("Transfer-Encoding") != "" {
		return -1
	}
	n, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	return n
}
