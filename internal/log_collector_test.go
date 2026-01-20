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
		lc := &LogCollector{enabled: true}

		handle := lc.StartCapture(context.Background())
		ctx := handle.Context()

		record := slog.Record{}
		record.Message = "test message"
		record.Level = slog.LevelInfo
		err := lc.Handle(ctx, record)
		assert.NoError(t, err)

		logs := handle.End()
		assert.NotNil(t, logs)
		assert.Len(t, logs, 1)
		assert.Equal(t, "test message", logs[0].Message)
		assert.Equal(t, "INFO", logs[0].Level)
	})

	t.Run("NoOpWhenDisabled", func(t *testing.T) {
		lc := &LogCollector{enabled: false}

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
		lc := &LogCollector{enabled: true}
		newHandler := lc.WithAttrs([]slog.Attr{slog.String("key", "value")})

		assert.NotSame(t, lc, newHandler)

		newCollector := newHandler.(*LogCollector)
		assert.True(t, newCollector.enabled)
	})

	t.Run("WithGroup", func(t *testing.T) {
		lc := &LogCollector{enabled: true}
		newHandler := lc.WithGroup("mygroup")

		assert.NotSame(t, lc, newHandler)

		newCollector := newHandler.(*LogCollector)
		assert.True(t, newCollector.enabled)
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
