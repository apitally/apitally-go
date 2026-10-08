package internal

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apitally/apitally-go/internal/testutils"
)

func TestSpoolRotatesFilesAtMaxSize(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	s := newSpool()
	payload := bytes.Repeat([]byte("x"), 1_000_000)

	for range 5 {
		s.append(signalTraces, payload)
	}

	files := s.pendingFiles()
	require.Len(t, files, 1)
	assert.Equal(t, 4_000_000, len(readSpoolFile(t, s, files[0])))
}

func TestSpoolEvictsOldestNonMetricsFilesFirstWhenFull(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	testutils.RecordSlog(t)
	s := newSpool()
	s.maxSize = 1000
	for _, signal := range []string{signalMetrics, signalLogs, signalTraces, signalMetrics} {
		s.append(signal, randomBytes(300))
		s.closeCurrentFiles()
	}

	s.append(signalLogs, randomBytes(10))

	var signals []string
	for _, f := range s.pendingFiles() {
		signals = append(signals, f.signal)
	}
	assert.Equal(t, []string{signalMetrics, signalTraces, signalMetrics}, signals)
}

func TestSpoolBuffersInMemoryWhenTempDirIsNotWritable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", dir)
	logs := testutils.RecordSlog(t)

	s := newSpool()
	s.append(signalLogs, []byte("payload"))
	s.closeCurrentFiles()

	files := s.pendingFiles()
	require.Len(t, files, 1)
	assert.Equal(t, []byte("payload"), readSpoolFile(t, s, files[0]))
	assert.Len(t, logs.Messages(slog.LevelWarn), 1)
	assert.NoDirExists(t, dir)
}

func TestSpoolDeletesOrphanedFilesUntouchedForTwoHours(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	orphaned, recent := filepath.Join(dir, "apitally-1.gz"), filepath.Join(dir, "apitally-2.gz")
	require.NoError(t, os.WriteFile(orphaned, nil, 0o600))
	require.NoError(t, os.WriteFile(recent, nil, 0o600))
	old := time.Now().Add(-maxUntouchedSpoolFileAge - time.Minute)
	require.NoError(t, os.Chtimes(orphaned, old, old))

	newSpool()

	assert.NoFileExists(t, orphaned)
	assert.FileExists(t, recent)
}

func TestSpoolDropsFilesPastRetention(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	testutils.RecordSlog(t)
	synctest.Test(t, func(t *testing.T) {
		s := newSpool()
		s.append(signalLogs, []byte("attempted"))
		s.closeCurrentFiles()
		_, ok := s.readForSend(s.pendingFiles()[0])
		require.True(t, ok)
		s.append(signalTraces, []byte("never attempted"))
		s.closeCurrentFiles()

		time.Sleep(maxRetryTimeAfterFirstAttempt + time.Second)
		s.rotateForExport()

		files := s.pendingFiles()
		require.Len(t, files, 1)
		assert.Equal(t, signalTraces, files[0].signal)
		s.deleteAll()
	})
}

func readSpoolFile(t *testing.T, s *spool, f *spoolFile) []byte {
	t.Helper()
	body, ok := s.readForSend(f)
	require.True(t, ok)
	reader, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return data
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
