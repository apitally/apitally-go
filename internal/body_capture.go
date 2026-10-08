package internal

import (
	"io"
	"net/http"
	"slices"
	"strings"
)

const maxBodySize = 50_000

var (
	bodyTooLarge        = []byte("[BODY_TOO_LARGE]")
	allowedContentTypes = []string{
		"application/json",
		"application/problem+json",
		"application/vnd.api+json",
		"application/ld+json",
		"application/x-ndjson",
		"text/markdown",
		"text/plain",
	}
)

// bodyCapture keeps a copy of up to maxBodySize body bytes. A body crossing
// the limit discards the copy and is captured as the too-large marker.
type bodyCapture struct {
	buffer     []byte
	isTooLarge bool
}

// newBodyCapture returns a capture that is already too large when the
// declared length exceeds the limit, so no bytes are copied.
func newBodyCapture(declaredLength int64) *bodyCapture {
	return &bodyCapture{isTooLarge: declaredLength > maxBodySize}
}

func (c *bodyCapture) write(p []byte) {
	if c.isTooLarge {
		return
	}
	if len(c.buffer)+len(p) > maxBodySize {
		c.buffer, c.isTooLarge = nil, true
		return
	}
	c.buffer = append(c.buffer, p...)
}

// body returns the captured bytes, or the too-large marker, which is
// exported even for incomplete bodies. Incomplete and empty bodies are not
// captured.
func (c *bodyCapture) body(isComplete bool) []byte {
	switch {
	case c == nil:
		return nil
	case c.isTooLarge:
		return bodyTooLarge
	case isComplete && len(c.buffer) > 0:
		return c.buffer
	}
	return nil
}

// bodyReader counts, and optionally captures, the bytes read from a body. It
// never reads more than its caller.
type bodyReader struct {
	reader  io.Reader
	size    int64
	isEOF   bool
	capture *bodyCapture
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.size += int64(n)
	if b.capture != nil {
		b.capture.write(p[:n])
	}
	if err == io.EOF {
		b.isEOF = true
	}
	return n, err
}

// isBodyCaptureAllowed decides from the headers alone whether a body can be
// captured: its content type is allowed and its encoding can be decoded.
func isBodyCaptureAllowed(header http.Header) bool {
	contentType := strings.ToLower(strings.TrimSpace(header.Get("Content-Type")))
	return slices.ContainsFunc(allowedContentTypes, func(allowed string) bool { return strings.HasPrefix(contentType, allowed) }) &&
		isSupportedContentEncoding(header.Get("Content-Encoding"))
}

func isSupportedContentEncoding(encoding string) bool {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity", "gzip", "deflate":
		return true
	}
	return false
}
