package internal

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	serverErrorEventName = "apitally.request.server_error"
	maxErrorGroups       = 100
	maxStackFrames       = 128
	maxExceptionType     = 256
	maxExceptionMessage  = 2_048
	maxExceptionStack    = 65_536
	maxErrorPath         = 2_000
	maxErrorConsumer     = 128
)

// capturedError holds the exception fields of a request's first captured
// error or panic.
type capturedError struct {
	typeName   string
	message    string
	stacktrace string
}

// CaptureError records err, returned through the framework's error channel,
// as the request's error unless one was already captured. Returned errors
// carry no stack trace.
func (s *RequestState) CaptureError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	if s.channelError == nil {
		s.channelError = err
	}
	s.mu.Unlock()
	s.captureError(err, "")
}

// CapturePanic records a recovered panic value with the panicking
// goroutine's stack. Call it from the deferred function that recovered.
func (s *RequestState) CapturePanic(recovered any) {
	if s == nil || recovered == nil || recovered == http.ErrAbortHandler {
		return
	}
	s.captureError(recovered, callerStack())
}

// captureError keeps only the first captured error and records it as the
// SERVER span's exception event. Cancellations are not errors of the
// application.
func (s *RequestState) captureError(value any, stacktrace string) {
	// Error and String methods of application types can panic, for example on
	// a typed nil error.
	defer recoverAndLogPanic("error capture")
	if err, ok := value.(error); ok && errors.Is(err, context.Canceled) {
		return
	}
	captured := &capturedError{
		typeName:   truncateString(exceptionTypeName(value), maxExceptionType),
		message:    strings.Clone(truncateString(strings.TrimSpace(exceptionMessage(value)), maxExceptionMessage)),
		stacktrace: stacktrace,
	}
	s.mu.Lock()
	isFirst := s.capturedError == nil
	if isFirst {
		s.capturedError = captured
	}
	s.mu.Unlock()
	if isFirst && s.span.IsRecording() {
		s.span.AddEvent("exception", trace.WithAttributes(
			attribute.String("exception.type", captured.typeName),
			attribute.String("exception.message", captured.message),
			attribute.String("exception.stacktrace", captured.stacktrace),
		))
	}
}

// exceptionTypeName returns the Go type name without one pointer level.
// Errors wrapped by fmt.Errorf with a single %w report the wrapped type.
func exceptionTypeName(value any) string {
	for {
		err, ok := value.(error)
		if !ok || reflect.TypeOf(err).String() != "*fmt.wrapError" {
			break
		}
		value = errors.Unwrap(err)
	}
	t := reflect.TypeOf(value)
	if t == nil {
		return "nil"
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.String()
}

func exceptionMessage(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	return fmt.Sprint(value)
}

// callerStack formats the calling goroutine's stack as "function\n\tfile:line"
// per frame, without goroutine IDs, arguments or offsets, so it is a stable
// aggregation key. Frames of the Go runtime and of Apitally are omitted. Called
// during a panic, the stack includes the panicking frames.
func callerStack() string {
	pcs := make([]uintptr, maxStackFrames)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(1, pcs)])
	var b strings.Builder
	for {
		frame, more := frames.Next()
		if !strings.HasPrefix(frame.Function, "runtime.") && !isSDKFunction(frame.Function) {
			line := frame.Function + "\n\t" + frame.File + ":" + strconv.Itoa(frame.Line)
			if b.Len()+len(line)+1 > maxExceptionStack {
				break
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
		}
		if !more {
			break
		}
	}
	return b.String()
}

// isSDKFunction reports whether function belongs to one of Apitally's
// packages, which are the module's direct subdirectories, excluding tests.
func isSDKFunction(function string) bool {
	// Type arguments of generic functions can contain other package paths.
	if i := strings.IndexByte(function, '['); i >= 0 {
		function = function[:i]
	}
	slash := strings.LastIndexByte(function, '/')
	dot := strings.IndexByte(function[slash+1:], '.')
	if slash < 0 || dot < 0 {
		return false
	}
	pkg := function[:slash+1+dot]
	return pkg[:slash] == rootModulePath && !strings.HasSuffix(pkg, "_test")
}

type serverErrorKey struct {
	method     string
	path       string
	typeName   string
	message    string
	stacktrace string
}

func serverErrorEventBody(key serverErrorKey, counts map[string]uint64) attribute.Value {
	return attribute.MapValue(
		attribute.String("method", key.method),
		attribute.String("path", key.path),
		attribute.String("type", key.typeName),
		attribute.String("message", key.message),
		attribute.String("stacktrace", key.stacktrace),
		attribute.Slice("counts", errorCounts(counts)...),
	)
}

// errorGroups counts distinct errors per consumer between drains. Errors
// beyond the limit are ignored; counts of retained errors are unlimited.
type errorGroups[K comparable] struct {
	mu     sync.Mutex
	groups map[K]map[string]uint64
}

// add counts an occurrence, where an empty consumer counts requests without
// a consumer.
func (g *errorGroups[K]) add(key K, consumer string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts := g.groups[key]
	if counts == nil {
		if len(g.groups) >= maxErrorGroups {
			return
		}
		if g.groups == nil {
			g.groups = map[K]map[string]uint64{}
		}
		counts = map[string]uint64{}
		g.groups[key] = counts
	}
	counts[consumer]++
}

func (g *errorGroups[K]) drain() map[K]map[string]uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	groups := g.groups
	g.groups = nil
	return groups
}

func errorCounts(counts map[string]uint64) []attribute.Value {
	values := make([]attribute.Value, 0, len(counts))
	for consumer, count := range counts {
		attrs := []attribute.KeyValue{attribute.Int64("count", int64(min(count, math.MaxUint32)))}
		if consumer != "" {
			attrs = append([]attribute.KeyValue{attribute.String("consumer", consumer)}, attrs...)
		}
		values = append(values, attribute.MapValue(attrs...))
	}
	return values
}

// isValidErrorMethod matches the server's rule for error events: 2 to 12
// uppercase letters or hyphens.
func isValidErrorMethod(method string) bool {
	if len(method) < 2 || len(method) > 12 {
		return false
	}
	for _, c := range method {
		if (c < 'A' || c > 'Z') && c != '-' {
			return false
		}
	}
	return true
}
