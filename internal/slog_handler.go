package internal

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	root "github.com/apitally/apitally-go"
)

const (
	maxLogTextLength          = 2_048
	maxBufferedLogsPerRequest = 1000
)

var (
	// isSlogHandlerCreated lets activation warn when logs cannot be captured.
	isSlogHandlerCreated atomic.Bool
	// Request-logging middleware logs every request with the request context,
	// which would duplicate the request log with unredacted query strings.
	requestLoggerFunctionPrefixes = []string{
		"github.com/go-chi/httplog/",
		"github.com/samber/slog-gin",
		"github.com/samber/slog-echo",
		"github.com/samber/slog-chi",
		"github.com/samber/slog-fiber",
	}
)

// NewSlogHandler returns a handler that captures records logged with the
// context of a monitored request and forwards every record to next
// unchanged.
func NewSlogHandler(next slog.Handler) slog.Handler {
	isSlogHandlerCreated.Store(true)
	return &slogHandler{next: next}
}

type slogHandler struct {
	next slog.Handler
	// operations are the WithAttrs and WithGroup calls on this handler, in order.
	operations []slogHandlerOperation
}

type slogHandlerOperation struct {
	attrs []slog.Attr
	group string
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *slogHandler) Handle(ctx context.Context, record slog.Record) error {
	h.capture(ctx, record)
	return h.next.Handle(ctx, record)
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogHandler{
		next:       h.next.WithAttrs(attrs),
		operations: append(slices.Clone(h.operations), slogHandlerOperation{attrs: ownedSlogAttrs(attrs)}),
	}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &slogHandler{
		next:       h.next.WithGroup(name),
		operations: append(slices.Clone(h.operations), slogHandlerOperation{group: name}),
	}
}

// capture buffers the record in its request. Records without a monitored
// request, including the SDK's own diagnostics, are not captured.
func (h *slogHandler) capture(ctx context.Context, record slog.Record) {
	defer recoverAndLogPanic("log capture")
	r := currentRuntime.Load()
	if r == nil || !r.active.Load() || !r.settings.config.CaptureLogs {
		return
	}
	state, spanCtx := r.logRequestState(ctx)
	if state == nil {
		return
	}
	frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
	if isRequestLoggerFunction(frame.Function) {
		return
	}
	if record.Time.IsZero() {
		record.Time = time.Now()
	}
	captured := &logRecord{
		Record:       root.LogRecord{Time: record.Time, Level: record.Level, Message: record.Message, Attrs: h.recordAttrs(record)},
		traceID:      spanCtx.TraceID(),
		spanID:       spanCtx.SpanID(),
		traceFlags:   spanCtx.TraceFlags(),
		serverSpanID: state.span.SpanContext().SpanID(),
		codeFunction: frame.Function,
		codeFile:     frame.File,
		codeLine:     frame.Line,
	}
	if mask := r.settings.config.MaskLogRecord; mask != nil && !callMaskLogRecord(mask, &captured.Record) {
		return
	}
	// The server drops records without a message.
	if captured.Record.Message == "" {
		return
	}
	captured.Record.Message = truncateString(toValidUTF8(captured.Record.Message), maxLogTextLength)
	state.logEmitted(captured)
}

func isRequestLoggerFunction(function string) bool {
	return slices.ContainsFunc(requestLoggerFunctionPrefixes, func(prefix string) bool { return strings.HasPrefix(function, prefix) })
}

// logRequestState finds the monitored request of the record's span, or the
// request state stored in a framework context that carries no span.
func (r *sdkRuntime) logRequestState(ctx context.Context) (*RequestState, trace.SpanContext) {
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		if state := r.registry.lookup(spanCtx.SpanID()); state != nil {
			return state, spanCtx
		}
	}
	if state := requestStateFromContext(ctx); state != nil && state.isMonitored && state.runtime == r {
		return state, state.span.SpanContext()
	}
	return nil, trace.SpanContext{}
}

// recordAttrs returns the handler's and the record's attributes, with groups
// as nested group values.
func (h *slogHandler) recordAttrs(record slog.Record) []slog.Attr {
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	attrs = ownedSlogAttrs(attrs)
	for i := len(h.operations) - 1; i >= 0; i-- {
		if operation := h.operations[i]; operation.group != "" {
			if len(attrs) > 0 {
				attrs = []slog.Attr{{Key: operation.group, Value: slog.GroupValue(attrs...)}}
			}
		} else {
			attrs = append(slices.Clone(operation.attrs), attrs...)
		}
	}
	return attrs
}

// callMaskLogRecord drops the record when the callback panics.
func callMaskLogRecord(mask func(*root.LogRecord) bool, record *root.LogRecord) (isKept bool) {
	defer func() {
		if p := recover(); p != nil {
			warnOnce("mask-log-record-panic", "Apitally MaskLogRecord callback panicked, log records are dropped", "panic", p)
			isKept = false
		}
	}()
	return mask(record)
}

// logEmitted buffers a captured record until the request is released.
// Records emitted after release are dropped.
func (s *RequestState) logEmitted(record *logRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isReleased && len(s.logs) < maxBufferedLogsPerRequest {
		s.logs = append(s.logs, record)
	}
}

// ownedSlogAttrs resolves LogValuers and replaces every KindAny value with
// a value the SDK owns, so captured records hold no references to
// application objects: maps become groups, slices and arrays become new
// []any of converted items, byte slices are copied and other types become
// strings.
func ownedSlogAttrs(attrs []slog.Attr) []slog.Attr {
	return ownedSlogAttrsAtDepth(attrs, 0)
}

// maxSlogValueDepth bounds the conversion of values that contain themselves.
const maxSlogValueDepth = 100

func ownedSlogAttrsAtDepth(attrs []slog.Attr, depth int) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		a.Value = ownedSlogValue(a.Value, depth)
		// Empty groups and empty attributes are omitted, as slog handlers do.
		if (a.Value.Kind() == slog.KindGroup && len(a.Value.Group()) == 0) || a.Equal(slog.Attr{}) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func ownedSlogValue(v slog.Value, depth int) slog.Value {
	if depth > maxSlogValueDepth {
		return slog.StringValue("<max-depth-exceeded>")
	}
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		return slog.GroupValue(ownedSlogAttrsAtDepth(v.Group(), depth+1)...)
	case slog.KindAny:
		return ownedAnyValue(v.Any(), depth)
	}
	return v
}

func ownedAnyValue(value any, depth int) slog.Value {
	switch value := value.(type) {
	case nil:
		return slog.AnyValue(nil)
	case []byte:
		return slog.AnyValue(bytes.Clone(value))
	case error:
		return slog.StringValue(value.Error())
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		attrs := make([]slog.Attr, 0, rv.Len())
		for iter := rv.MapRange(); iter.Next(); {
			attrs = append(attrs, slog.Attr{Key: fmt.Sprintf("%+v", iter.Key().Interface()), Value: ownedSlogValue(slog.AnyValue(iter.Value().Interface()), depth+1)})
		}
		slices.SortFunc(attrs, func(a, b slog.Attr) int { return strings.Compare(a.Key, b.Key) })
		return slog.GroupValue(attrs...)
	case reflect.Slice, reflect.Array:
		items := make([]any, rv.Len())
		for i := range rv.Len() {
			items[i] = ownedSlogValue(slog.AnyValue(rv.Index(i).Interface()), depth+1).Any()
		}
		return slog.AnyValue(items)
	case reflect.Pointer:
		if rv.IsNil() {
			return slog.AnyValue(nil)
		}
		return ownedSlogValue(slog.AnyValue(rv.Elem().Interface()), depth+1)
	}
	return slog.StringValue(fmt.Sprintf("%+v", value))
}

// slogAttributes converts attributes to OTLP attribute values following the
// otelslog bridge. Values set by MaskLogRecord are converted first, and
// strings are truncated.
func slogAttributes(attrs []slog.Attr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, attribute.KeyValue{Key: attribute.Key(toValidUTF8(a.Key)), Value: slogAttributeValue(ownedSlogValue(a.Value, 0))})
	}
	return out
}

func slogAttributeValue(v slog.Value) attribute.Value {
	switch v.Kind() {
	case slog.KindString:
		return attribute.StringValue(truncateString(toValidUTF8(v.String()), maxLogTextLength))
	case slog.KindInt64:
		return attribute.Int64Value(v.Int64())
	case slog.KindUint64:
		if n := v.Uint64(); n <= math.MaxInt64 {
			return attribute.Int64Value(int64(n))
		}
		return attribute.StringValue(strconv.FormatUint(v.Uint64(), 10))
	case slog.KindFloat64:
		return attribute.Float64Value(v.Float64())
	case slog.KindBool:
		return attribute.BoolValue(v.Bool())
	case slog.KindDuration:
		return attribute.Int64Value(v.Duration().Nanoseconds())
	case slog.KindTime:
		return attribute.Int64Value(v.Time().UnixNano())
	case slog.KindGroup:
		return attribute.MapValue(slogAttributes(v.Group())...)
	}
	switch value := v.Any().(type) {
	case []byte:
		return attribute.ByteSliceValue(value)
	case []any:
		items := make([]attribute.Value, len(value))
		for i, item := range value {
			items[i] = slogAttributeValue(slog.AnyValue(item))
		}
		return attribute.SliceValue(items...)
	}
	return attribute.Value{}
}

// slogSeverityNumber maps slog levels to OTLP severity numbers as the
// otelslog bridge does: Debug 5, Info 9, Warn 13, Error 17.
func slogSeverityNumber(level slog.Level) int32 {
	return int32(min(max(int(level)+9, 1), 24))
}
