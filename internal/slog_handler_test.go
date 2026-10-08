package internal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestRequestLogsAreLinkedToServerSpanAndForwarded(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	var output bytes.Buffer
	logger := slog.New(NewSlogHandler(slog.NewTextHandler(&output, nil)))
	var childSpanID trace.SpanID
	var logFile string
	var logLine int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		_, logFile, logLine, _ = runtime.Caller(0)
		logger.InfoContext(r.Context(), "listing items", "count", 2)
		ctx, span := otel.Tracer("test").Start(r.Context(), "query")
		childSpanID = span.SpanContext().SpanID()
		logger.WarnContext(ctx, "slow query")
		span.End()
		_ = logger.Handler().Handle(r.Context(), slog.NewRecord(time.Now(), slog.LevelInfo, "without code location", 0))
		logger.Info("without request context")
	})
	appURL := startTestApp(t, mux)

	logger.InfoContext(context.Background(), "outside request")
	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, 5, strings.Count(output.String(), "\n"))
	spans := server.Spans(t)
	rootSpan := findSpan(t, spans, "GET /items")
	slogRecords := server.ApplicationLogRecords(t)
	require.Len(t, slogRecords, 3)
	first, second, third := slogRecords[0], slogRecords[1], slogRecords[2]
	assert.Equal(t, "listing items", first.Body.GetStringValue())
	assert.Equal(t, logspb.SeverityNumber_SEVERITY_NUMBER_INFO, first.SeverityNumber)
	assert.Equal(t, rootSpan.TraceId, first.TraceId)
	assert.Equal(t, rootSpan.SpanId, first.SpanId)
	attrs := testutils.Attributes(first.Attributes)
	assert.Equal(t, trace.SpanID(rootSpan.SpanId).String(), attrs["apitally.request.server_span_id"])
	assert.Equal(t, "github.com/apitally/apitally-go/internal.TestRequestLogsAreLinkedToServerSpanAndForwarded.func1", attrs["code.function.name"])
	assert.Equal(t, logFile, attrs["code.file.path"])
	assert.Equal(t, int64(logLine+1), attrs["code.line.number"])
	assert.Equal(t, int64(2), attrs["count"])
	assert.Equal(t, "slow query", second.Body.GetStringValue())
	assert.Equal(t, logspb.SeverityNumber_SEVERITY_NUMBER_WARN, second.SeverityNumber)
	assert.Equal(t, childSpanID[:], second.SpanId)
	assert.Equal(t, trace.SpanID(rootSpan.SpanId).String(), testutils.Attributes(second.Attributes)["apitally.request.server_span_id"])
	assert.Equal(t, map[string]any{"apitally.request.server_span_id": trace.SpanID(rootSpan.SpanId).String()}, testutils.Attributes(third.Attributes))
}

func TestMaskLogRecordEditsAndDropsCapturedRecordsOnly(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	cfg := root.NewConfig()
	cfg.MaskLogRecord = func(record *root.LogRecord) bool {
		switch record.Message {
		case "drop":
			return false
		case "panic":
			panic("bug")
		case "empty":
			record.Message = ""
		}
		record.Attrs = slices.DeleteFunc(record.Attrs, func(a slog.Attr) bool { return a.Key == "password" })
		return true
	}
	registerForTest(t, server, cfg)
	var output bytes.Buffer
	logger := slog.New(NewSlogHandler(slog.NewTextHandler(&output, nil)))
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, message := range []string{"login", "drop", "panic", "empty"} {
			logger.InfoContext(r.Context(), message, "password", "secret", "user", "alice")
		}
	}))

	testutils.Get(t, appURL+"/login")
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, 4, strings.Count(output.String(), "password=secret"))
	var bodies []string
	for _, record := range server.ApplicationLogRecords(t) {
		bodies = append(bodies, record.Body.GetStringValue())
		assert.NotContains(t, testutils.Attributes(record.Attributes), "password")
		assert.Equal(t, "alice", testutils.Attributes(record.Attributes)["user"])
	}
	assert.Equal(t, []string{"login"}, bodies)
}

type loginValuer struct{ user string }

func (v loginValuer) LogValue() slog.Value { return slog.StringValue("user " + v.user) }

type pointerError struct{ message string }

func (e *pointerError) Error() string { return e.message }

func TestCapturedValuesAreConvertedAndTruncated(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	var callbackMapKind slog.Kind
	cfg := root.NewConfig()
	cfg.MaskLogRecord = func(record *root.LogRecord) bool {
		callbackMapKind = record.Attrs[2].Value.Group()[0].Value.Kind()
		return true
	}
	registerForTest(t, server, cfg)
	logger := slog.New(NewSlogHandler(slog.NewTextHandler(io.Discard, nil))).With("service", "shop", "cause", (*pointerError)(nil)).WithGroup("request")
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), strings.Repeat("é", maxLogTextLength+1),
			"map", map[string]int{"a": 1},
			"list", []any{1, "two"},
			"struct", struct{ A int }{1},
			"error", errors.New("failed"),
			"bytes", []byte("raw"),
			"valuer", loginValuer{"alice"},
			"long", strings.Repeat("x", maxLogTextLength+1),
		)
	}))

	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, slog.KindGroup, callbackMapKind)
	records := server.ApplicationLogRecords(t)
	require.Len(t, records, 1)
	assert.Equal(t, strings.Repeat("é", maxLogTextLength), records[0].Body.GetStringValue())
	attrs := testutils.Attributes(records[0].Attributes)
	assert.Equal(t, "shop", attrs["service"])
	assert.Equal(t, "<nil>", attrs["cause"])
	assert.Equal(t, map[string]any{
		"map":    map[string]any{"a": int64(1)},
		"list":   []any{int64(1), "two"},
		"struct": "{A:1}",
		"error":  "failed",
		"bytes":  []byte("raw"),
		"valuer": "user alice",
		"long":   strings.Repeat("x", maxLogTextLength),
	}, attrs["request"])
}

func TestRequestBuffersAtMostThousandLogRecords(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	registerForTest(t, server, nil)
	appURL := startTestApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range maxBufferedLogsPerRequest + 1 {
			slog.InfoContext(r.Context(), "query")
		}
		writeOK(w, r)
	}))

	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Len(t, server.ApplicationLogRecords(t), maxBufferedLogsPerRequest)
}
