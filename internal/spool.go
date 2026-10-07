package internal

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	signalTraces  = "traces"
	signalLogs    = "logs"
	signalMetrics = "metrics"

	maxUncompressedSpoolFileSize = 4_000_000
	maxSpoolSizeOnDisk           = 50_000_000
	maxSpoolSizeInMemory         = 10_000_000
	// Apitally deduplicates retries for one hour after the first attempt.
	maxRetryTimeAfterFirstAttempt = 59 * time.Minute
	maxUntouchedSpoolFileAge      = 2 * time.Hour
	spoolFilePattern              = "apitally-*.gz"
)

var spoolSignals = []string{signalTraces, signalLogs, signalMetrics}

// spool stores encoded OTLP payloads as one gzip stream per file and signal.
// A closed file is sent verbatim as one request body, so retries are
// byte-identical.
type spool struct {
	mu                     sync.Mutex
	inMemory               bool
	current                map[string]*spoolFile
	closed                 []*spoolFile
	filesClosedSinceRotate int
	isWriteErrorLogged     bool
	maxSize                int64
}

type spoolFile struct {
	signal           string
	path             string
	memory           *bytes.Buffer
	file             *os.File
	gzip             *gzip.Writer
	storedSize       int64
	uncompressedSize int
	firstAttempt     time.Time
}

func newSpool() *spool {
	s := &spool{current: map[string]*spoolFile{}, maxSize: maxSpoolSizeOnDisk}
	if isTempDirWritable() {
		deleteOrphanedSpoolFiles()
	} else {
		s.inMemory, s.maxSize = true, maxSpoolSizeInMemory
		logWarn("Unable to create temporary files, Apitally buffers telemetry in memory (up to 10 MB)")
	}
	return s
}

// appendMessage encodes m and appends it. Encoding fails only for invalid
// UTF-8 in string fields, which would make the whole export unparseable.
func (s *spool) appendMessage(signal string, m proto.Message) {
	payload, err := proto.Marshal(m)
	if err != nil {
		warnOnce("encode-"+signal, "Apitally could not encode "+signal+" and dropped them", "error", err)
		return
	}
	s.append(signal, payload)
}

func (s *spool) append(signal string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.current[signal]
	if f != nil && f.uncompressedSize+len(payload) > maxUncompressedSpoolFileSize {
		s.closeCurrentFileLocked(signal)
		f = nil
	}
	err := func() (err error) {
		if f == nil {
			if f, err = s.createFile(signal); err != nil {
				return err
			}
			s.current[signal] = f
		}
		return f.write(payload)
	}()
	if err != nil {
		s.logWriteError(signal, err)
		if f != nil {
			delete(s.current, signal)
			f.delete()
		}
	} else {
		s.isWriteErrorLogged = false
	}
	s.enforceSizeLimitLocked()
}

// rotateForExport closes each signal's current file unless closed files of the
// signal are already waiting, so an outage grows the current file instead of
// producing one file per cycle. It returns the number of files closed since
// the previous call, including files closed for size.
func (s *spool) rotateForExport() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, signal := range spoolSignals {
		if s.current[signal] != nil && !slices.ContainsFunc(s.closed, func(f *spoolFile) bool { return f.signal == signal }) {
			s.closeCurrentFileLocked(signal)
		}
	}
	s.closed = slices.DeleteFunc(s.closed, s.deleteIfExpired)
	s.enforceSizeLimitLocked()
	n := s.filesClosedSinceRotate
	s.filesClosedSinceRotate = 0
	return n
}

func (s *spool) closeCurrentFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, signal := range spoolSignals {
		if s.current[signal] != nil {
			s.closeCurrentFileLocked(signal)
		}
	}
}

// pendingFiles returns the closed files, oldest first.
func (s *spool) pendingFiles() []*spoolFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.closed)
}

// readForSend marks a send attempt and returns the file's stored bytes. It
// returns false when the file was deleted or has expired, which deletes it.
func (s *spool) readForSend(f *spoolFile) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.closed, f) {
		return nil, false
	}
	if s.deleteIfExpired(f) {
		s.closed = slices.DeleteFunc(s.closed, func(c *spoolFile) bool { return c == f })
		return nil, false
	}
	if f.firstAttempt.IsZero() {
		f.firstAttempt = time.Now()
	}
	body, err := f.read()
	if err != nil {
		s.logWriteError(f.signal, err)
		s.deleteLocked(f)
		return nil, false
	}
	return body, true
}

func (s *spool) delete(f *spoolFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteLocked(f)
}

// touchFiles refreshes modification times, so orphan cleanup by other
// processes keeps the files of this live process.
func (s *spool) touchFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, f := range s.current {
		f.touch(now)
	}
	for _, f := range s.closed {
		f.touch(now)
	}
}

func (s *spool) deleteAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for signal, f := range s.current {
		delete(s.current, signal)
		f.delete()
	}
	for _, f := range s.closed {
		f.delete()
	}
	s.closed = nil
}

func (s *spool) createFile(signal string) (*spoolFile, error) {
	f := &spoolFile{signal: signal}
	var w io.Writer
	if s.inMemory {
		f.memory = &bytes.Buffer{}
		w = f.memory
	} else {
		file, err := os.CreateTemp("", spoolFilePattern)
		if err != nil {
			return nil, err
		}
		f.file, f.path = file, file.Name()
		w = file
	}
	f.gzip = gzip.NewWriter(&countingWriter{w: w, n: &f.storedSize})
	return f, nil
}

func (s *spool) closeCurrentFileLocked(signal string) {
	f := s.current[signal]
	delete(s.current, signal)
	if err := f.close(); err != nil {
		s.logWriteError(signal, err)
		f.delete()
		return
	}
	s.closed = append(s.closed, f)
	s.filesClosedSinceRotate++
}

func (s *spool) deleteLocked(f *spoolFile) {
	s.closed = slices.DeleteFunc(s.closed, func(c *spoolFile) bool { return c == f })
	f.delete()
}

func (s *spool) deleteIfExpired(f *spoolFile) bool {
	if f.firstAttempt.IsZero() || time.Since(f.firstAttempt) <= maxRetryTimeAfterFirstAttempt {
		return false
	}
	logWarn("Apitally could not deliver buffered " + f.signal + " within an hour and dropped them")
	f.delete()
	return true
}

// enforceSizeLimitLocked evicts the oldest closed files, other signals before
// metrics, because metrics carry the liveness signal.
func (s *spool) enforceSizeLimitLocked() {
	for s.totalSizeLocked() > s.maxSize && len(s.closed) > 0 {
		i := slices.IndexFunc(s.closed, func(f *spoolFile) bool { return f.signal != signalMetrics })
		oldest := s.closed[max(i, 0)]
		logWarn("Apitally telemetry buffer is full, dropping the oldest buffered " + oldest.signal)
		s.deleteLocked(oldest)
	}
}

func (s *spool) totalSizeLocked() int64 {
	var total int64
	for _, f := range s.current {
		total += f.storedSize
	}
	for _, f := range s.closed {
		total += f.storedSize
	}
	return total
}

// logWriteError logs once until writes succeed again.
func (s *spool) logWriteError(signal string, err error) {
	if !s.isWriteErrorLogged {
		s.isWriteErrorLogged = true
		logWarn("Apitally could not write buffered "+signal+" and dropped them", "error", err)
	}
}

func (f *spoolFile) write(payload []byte) error {
	if _, err := f.gzip.Write(payload); err != nil {
		return err
	}
	f.uncompressedSize += len(payload)
	return nil
}

func (f *spoolFile) close() error {
	err := f.gzip.Close()
	if f.file != nil {
		if closeErr := f.file.Close(); err == nil {
			err = closeErr
		}
		f.file = nil
	}
	return err
}

func (f *spoolFile) read() ([]byte, error) {
	if f.memory != nil {
		return f.memory.Bytes(), nil
	}
	return os.ReadFile(f.path)
}

func (f *spoolFile) touch(now time.Time) {
	if f.path != "" {
		_ = os.Chtimes(f.path, now, now)
	}
}

func (f *spoolFile) delete() {
	if f.file != nil {
		_ = f.file.Close()
		f.file = nil
	}
	if f.path != "" {
		_ = os.Remove(f.path)
	}
	f.memory = nil
}

type countingWriter struct {
	w io.Writer
	n *int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	*c.n += int64(n)
	return n, err
}

func isTempDirWritable() bool {
	f, err := os.CreateTemp("", spoolFilePattern)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return true
}

// deleteOrphanedSpoolFiles removes spool files that no live process has
// touched recently, left behind by processes that exited without a final drain.
func deleteOrphanedSpoolFiles() {
	paths, _ := filepath.Glob(filepath.Join(os.TempDir(), spoolFilePattern))
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > maxUntouchedSpoolFileAge {
			_ = os.Remove(path)
		}
	}
}
