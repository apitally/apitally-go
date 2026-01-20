package internal

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogCollector(t *testing.T) {
	t.Run("CaptureLogsWhenEnabled", func(t *testing.T) {
		originalHandler := slog.Default().Handler()
		t.Cleanup(func() { slog.SetDefault(slog.New(originalHandler)) })

		lc := NewLogCollector(true)

		handle := lc.StartCapture(context.Background())
		ctx := handle.Context()

		slog.InfoContext(ctx, "test message")
		slog.InfoContext(ctx, "user logged in", "user_id", 123, "method", "oauth")
		slog.InfoContext(ctx, "request processed", slog.Group("request", "path", "/api/users", "method", "GET"))

		logs := handle.End()
		assert.NotNil(t, logs)
		assert.Len(t, logs, 3)

		assert.Equal(t, "test message", logs[0].Message)
		assert.Equal(t, "INFO", logs[0].Level)
		assert.NotEmpty(t, logs[0].File)
		assert.NotZero(t, logs[0].Line)
		assert.NotEmpty(t, logs[0].Logger)

		assert.True(t, strings.HasPrefix(logs[1].Message, "user logged in\n"))
		assert.Contains(t, logs[1].Message, "user_id=123")
		assert.Contains(t, logs[1].Message, "method=oauth")

		assert.True(t, strings.HasPrefix(logs[2].Message, "request processed\n"))
		assert.Contains(t, logs[2].Message, "request.path=/api/users")
		assert.Contains(t, logs[2].Message, "request.method=GET")
	})

	t.Run("NoOpWhenDisabled", func(t *testing.T) {
		lc := NewLogCollector(false)

		handle := lc.StartCapture(context.Background())
		ctx := handle.Context()

		record := slog.Record{}
		record.Message = "test message"
		record.Level = slog.LevelInfo
		err := lc.Handle(ctx, record)
		assert.NoError(t, err)

		logs := handle.End()
		assert.Nil(t, logs)
	})

	t.Run("Enabled", func(t *testing.T) {
		lc := &LogCollector{enabled: true}
		assert.True(t, lc.Enabled(context.Background(), slog.LevelInfo))
		assert.True(t, lc.Enabled(context.Background(), slog.LevelWarn))
		assert.False(t, lc.Enabled(context.Background(), slog.LevelDebug))
	})

	t.Run("WithAttrs", func(t *testing.T) {
		originalHandler := slog.Default().Handler()
		t.Cleanup(func() { slog.SetDefault(slog.New(originalHandler)) })

		lc := NewLogCollector(true)
		newHandler := lc.WithAttrs([]slog.Attr{slog.String("key", "value")})

		assert.NotSame(t, lc, newHandler)

		newCollector := newHandler.(*LogCollector)
		assert.True(t, newCollector.enabled)
		assert.NotNil(t, newCollector.next)
	})

	t.Run("WithGroup", func(t *testing.T) {
		originalHandler := slog.Default().Handler()
		t.Cleanup(func() { slog.SetDefault(slog.New(originalHandler)) })

		lc := NewLogCollector(true)
		newHandler := lc.WithGroup("mygroup")

		assert.NotSame(t, lc, newHandler)

		newCollector := newHandler.(*LogCollector)
		assert.True(t, newCollector.enabled)
		assert.NotNil(t, newCollector.next)
	})

	t.Run("TruncateMessage", func(t *testing.T) {
		short := "hello"
		assert.Equal(t, short, truncateLogMessage(short))

		long := strings.Repeat("x", maxLogMsgLength+100)
		truncated := truncateLogMessage(long)
		assert.Len(t, truncated, maxLogMsgLength)
		assert.True(t, strings.HasSuffix(truncated, "... (truncated)"))
	})
}
