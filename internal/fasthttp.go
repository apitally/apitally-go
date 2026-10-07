package internal

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

// FasthttpObservation observes a Fiber request until fasthttp finishes or
// aborts writing its response. fasthttp closes io.Closer user values when it
// resets the request context, which completes observation.
type FasthttpObservation struct {
	State *RequestState

	mu                sync.Mutex
	result            *TransportResult
	stream            *responseStream
	isClosed          bool
	isCloseRegistered bool
	finishOnce        sync.Once
}

type fasthttpObservationKey struct{}

// BeginFasthttp activates Apitally and begins the request. requestCtx is the
// *fasthttp.RequestCtx, which closes the observation as a user value.
func BeginFasthttp(ctx context.Context, requestCtx any, info RequestInfo) (*FasthttpObservation, context.Context) {
	Activate()
	state, ctx := BeginRequest(ctx, info)
	o := &FasthttpObservation{State: state}
	if userValues, ok := requestCtx.(interface{ SetUserValue(key, value any) }); ok && state != nil {
		userValues.SetUserValue(fasthttpObservationKey{}, o)
		o.isCloseRegistered = true
	}
	return o, ctx
}

// FinishHandler records the response data after the handler chain returned
// or panicked. result holds the route, status, client address and a copy of
// the response headers. request and response are the *fasthttp.Request and
// *fasthttp.Response, read before Fiber reuses them. recovered is the value of
// a panic unwinding the handler chain, or nil.
func (o *FasthttpObservation) FinishHandler(result TransportResult, request, response any, recovered any) {
	if o.State == nil {
		return
	}
	o.State.CapturePanic(recovered)
	defer recoverAndLogPanic("request observation")
	if recovered != nil {
		// A fasthttp response is buffered, so a panicking handler sent nothing yet.
		result.StatusCode = http.StatusInternalServerError
	}
	result.RequestBodySize = o.State.info.ContentLength
	if req, ok := request.(interface {
		IsBodyStream() bool
		Body() []byte
	}); ok && !req.IsBodyStream() {
		body := req.Body()
		result.RequestBodySize = int64(len(body))
		if o.State.shouldCaptureRequestBody() {
			result.RequestBody = capturedCopy(body)
		}
	}
	result.ResponseBodySize = -1
	isCaptured := o.State.shouldCaptureResponseBody(result.ResponseHeader)
	if resp, ok := response.(interface {
		IsBodyStream() bool
		BodyStream() io.Reader
		Body() []byte
	}); ok {
		if !resp.IsBodyStream() {
			body := resp.Body()
			result.ResponseBodySize = int64(len(body))
			if isCaptured {
				result.ResponseBody = capturedCopy(body)
			}
		} else if size := streamSize(resp.BodyStream(), result.ResponseHeader); size >= 0 {
			result.ResponseBodySize = size
		} else {
			stream := &responseStream{}
			if isCaptured {
				stream.capture = newBodyCapture(-1)
			}
			if replaceResponseBodyStream(response, stream.wrap) {
				o.stream = stream
			}
		}
	}
	o.mu.Lock()
	o.result = &result
	isComplete := o.isClosed || !o.isCloseRegistered
	o.mu.Unlock()
	if isComplete {
		o.finish()
	}
}

// Close completes observation after fasthttp wrote or aborted the response.
func (o *FasthttpObservation) Close() error {
	o.mu.Lock()
	o.isClosed = true
	isComplete := o.result != nil
	o.mu.Unlock()
	if isComplete {
		o.finish()
	}
	return nil
}

func (o *FasthttpObservation) finish() {
	o.finishOnce.Do(func() {
		result := *o.result
		if s := o.stream; s != nil {
			if s.isEOF {
				result.ResponseBodySize = s.size
			}
			result.ResponseBody = s.capture.body(s.isEOF)
		}
		o.State.FinishObservation(result)
	})
}

// HeaderFromValues copies header values, such as those of Fiber's
// GetReqHeaders, which may reference buffers Fiber reuses.
func HeaderFromValues(values map[string][]string) http.Header {
	header := make(http.Header, len(values))
	for name, items := range values {
		for _, value := range items {
			header.Add(strings.Clone(name), strings.Clone(value))
		}
	}
	return header
}

func capturedCopy(body []byte) []byte {
	capture := newBodyCapture(int64(len(body)))
	capture.write(body)
	return capture.body(true)
}

// streamSize returns the length of a response stream that is known without
// reading it, or -1.
func streamSize(stream io.Reader, header http.Header) int64 {
	if size := declaredContentLength(header); size >= 0 {
		return size
	}
	switch stream := stream.(type) {
	case *io.LimitedReader:
		return max(stream.N, 0)
	case *bytes.Reader:
		return int64(stream.Len())
	case *bytes.Buffer:
		return int64(stream.Len())
	}
	return -1
}

// bodyStreamFields maps a response type to the index of its bodyStream
// field, or nil when the field is missing or not an io.Reader.
var bodyStreamFields sync.Map

// replaceResponseBodyStream replaces the body stream of a *fasthttp.Response
// with wrap's result. fasthttp has no public way to replace a stream without
// closing it, so this writes the unexported bodyStream field, whose name and
// type are checked once per process. It reports false when the layout
// differs.
func replaceResponseBodyStream(response any, wrap func(io.Reader) io.Reader) bool {
	value := reflect.ValueOf(response)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return false
	}
	index, isChecked := bodyStreamFields.Load(value.Type())
	if !isChecked {
		field, ok := value.Elem().Type().FieldByName("bodyStream")
		if ok && field.Type == reflect.TypeFor[io.Reader]() {
			index = field.Index
		} else {
			index = []int(nil)
			logWarn("Apitally cannot observe streamed Fiber responses of unknown length with this fasthttp version, so their size and body are omitted")
		}
		bodyStreamFields.Store(value.Type(), index)
	}
	if index.([]int) == nil {
		return false
	}
	stream := (*io.Reader)(unsafe.Pointer(value.Elem().FieldByIndex(index.([]int)).UnsafeAddr()))
	*stream = wrap(*stream)
	return true
}

// responseStream counts, and optionally captures, the bytes fasthttp reads
// from a response stream while writing the response.
type responseStream struct {
	reader  io.Reader
	size    int64
	isEOF   bool
	capture *bodyCapture
}

func (s *responseStream) Read(p []byte) (int, error) {
	n, err := s.reader.Read(p)
	s.size += int64(n)
	if s.capture != nil {
		s.capture.write(p[:n])
	}
	if err == io.EOF {
		s.isEOF = true
	}
	return n, err
}

// wrap returns s with exactly the Close and CloseWithError methods of the
// original stream, because fasthttp calls each method the stream has.
func (s *responseStream) wrap(original io.Reader) io.Reader {
	s.reader = original
	_, hasClose := original.(io.Closer)
	_, hasCloseWithError := original.(interface{ CloseWithError(error) error })
	switch {
	case hasClose && hasCloseWithError:
		return streamWithBothCloses{streamWithClose{s}}
	case hasClose:
		return streamWithClose{s}
	case hasCloseWithError:
		return streamWithCloseWithError{s}
	}
	return s
}

type streamWithClose struct {
	*responseStream
}

func (s streamWithClose) Close() error {
	return s.reader.(io.Closer).Close()
}

type streamWithCloseWithError struct {
	*responseStream
}

func (s streamWithCloseWithError) CloseWithError(err error) error {
	return s.reader.(interface{ CloseWithError(error) error }).CloseWithError(err)
}

type streamWithBothCloses struct {
	streamWithClose
}

func (s streamWithBothCloses) CloseWithError(err error) error {
	return streamWithCloseWithError(s.streamWithClose).CloseWithError(err)
}
