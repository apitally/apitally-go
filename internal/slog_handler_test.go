package internal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "listing items", "count", 2)
		ctx, span := otel.Tracer("test").Start(r.Context(), "query")
		childSpanID = span.SpanContext().SpanID()
		logger.WarnContext(ctx, "slow query")
		span.End()
		logger.Info("without request context")
	})
	appURL := startTestApp(t, mux)

	logger.InfoContext(context.Background(), "outside request")
	testutils.Get(t, appURL+"/items")
	require.NoError(t, Shutdown(context.Background()))

	assert.Equal(t, 4, strings.Count(output.String(), "\n"))
	spans := server.Spans(t)
	rootSpan := findSpan(t, spans, "GET /items")
	records := server.LogRecords(t)
	slogRecords := slices.DeleteFunc(slices.Clone(records), func(r testutils.LogRecord) bool { return r.Scope != "slog" })
	require.Len(t, slogRecords, 2)
	first, second := slogRecords[0], slogRecords[1]
	assert.Equal(t, "listing items", first.Body.GetStringValue())
	assert.Equal(t, logspb.SeverityNumber_SEVERITY_NUMBER_INFO, first.SeverityNumber)
	assert.Equal(t, rootSpan.TraceId, first.TraceId)
	assert.Equal(t, rootSpan.SpanId, first.SpanId)
	attrs := testutils.Attributes(first.Attributes)
	assert.Equal(t, trace.SpanID(rootSpan.SpanId).String(), attrs["apitally.request.server_span_id"])
	assert.Equal(t, "github.com/apitally/apitally-go/internal.TestRequestLogsAreLinkedToServerSpanAndForwarded.func1", attrs["code.function.name"])
	assert.True(t, strings.HasSuffix(attrs["code.file.path"].(string), "/internal/slog_handler_test.go"))
	assert.Positive(t, attrs["code.line.number"])
	assert.Equal(t, int64(2), attrs["count"])
	assert.Equal(t, "slow query", second.Body.GetStringValue())
	assert.Equal(t, logspb.SeverityNumber_SEVERITY_NUMBER_WARN, second.SeverityNumber)
	assert.Equal(t, childSpanID[:], second.SpanId)
	assert.Equal(t, trace.SpanID(rootSpan.SpanId).String(), testutils.Attributes(second.Attributes)["apitally.request.server_span_id"])
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
	for _, record := range server.LogRecords(t) {
		if record.Scope == "slog" {
			bodies = append(bodies, record.Body.GetStringValue())
			assert.NotContains(t, testutils.Attributes(record.Attributes), "password")
			assert.Equal(t, "alice", testutils.Attributes(record.Attributes)["user"])
		}
	}
	assert.Equal(t, []string{"login"}, bodies)
}

type loginValuer struct{ user string }

func (v loginValuer) LogValue() slog.Value { return slog.StringValue("user " + v.user) }

func TestCapturedValuesAreConvertedAndTruncated(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	var callbackMapKind slog.Kind
	cfg := root.NewConfig()
	cfg.MaskLogRecord = func(record *root.LogRecord) bool {
		callbackMapKind = record.Attrs[1].Value.Group()[0].Value.Kind()
		return true
	}
	registerForTest(t, server, cfg)
	logger := slog.New(NewSlogHandler(slog.NewTextHandler(io.Discard, nil))).With("service", "shop").WithGroup("request")
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
	records := slices.DeleteFunc(server.LogRecords(t), func(r testutils.LogRecord) bool { return r.Scope != "slog" })
	require.Len(t, records, 1)
	assert.Equal(t, strings.Repeat("é", maxLogTextLength), records[0].Body.GetStringValue())
	attrs := testutils.Attributes(records[0].Attributes)
	assert.Equal(t, "shop", attrs["service"])
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

func TestRequestLoggingMiddlewareIsRecognizedByFunctionName(t *testing.T) {
	assert.True(t, isRequestLoggerFunction("github.com/samber/slog-gin.NewWithConfig.func1"))
	assert.True(t, isRequestLoggerFunction("github.com/go-chi/httplog/v3.RequestLogger.func1.1"))
	assert.False(t, isRequestLoggerFunction("main.listItems"))
}

func TestActivationWarnsWhenNoSlogHandlerWasCreated(t *testing.T) {
	for _, captureLogs := range []bool{true, false} {
		t.Run(strconv.FormatBool(captureLogs), func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			SetUpTest(t)
			setExportTransportForTest(t, server.Transport())
			logs := testutils.RecordSlog(t)
			cfg := root.NewConfig()
			cfg.CaptureLogs = captureLogs
			Register(cfg, testFramework, nil)

			Activate()
			require.NoError(t, Shutdown(context.Background()))

			assert.Equal(t, captureLogs, slices.ContainsFunc(logs.Messages(slog.LevelWarn), func(msg string) bool { return strings.Contains(msg, "NewSlogHandler") }))
		})
	}
}
