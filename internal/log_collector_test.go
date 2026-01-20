package internal

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogCollector(t *testing.T) {
	t.Run("Disabled", func(t *testing.T) {
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

	t.Run("TruncateMessage", func(t *testing.T) {
		short := "hello"
		assert.Equal(t, short, truncateLogMessage(short))

		long := strings.Repeat("x", maxLogMsgLength+100)
		truncated := truncateLogMessage(long)
		assert.Len(t, truncated, maxLogMsgLength)
		assert.True(t, strings.HasSuffix(truncated, "... (truncated)"))
	})
}
