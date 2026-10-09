package internal

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// processBody turns a captured body into its attribute value: bounded
// decompression, then the mask callback, then JSON field redaction with
// compact serialization. It returns false when the body is omitted. Failures
// fail closed with the redacted marker.
func (red *redaction) processBody(span sdktrace.ReadOnlySpan, body []byte, encoding string, option string, mask func(sdktrace.ReadOnlySpan, []byte) []byte) (attribute.Value, bool) {
	switch {
	case body == nil:
		return attribute.Value{}, false
	case bytes.Equal(body, bodyTooLarge):
		return attribute.StringValue(string(bodyTooLarge)), true
	}
	body, err := decompressBody(body, encoding)
	switch {
	case errors.Is(err, errBodyTooLarge):
		return attribute.StringValue(string(bodyTooLarge)), true
	case err != nil:
		return attribute.StringValue(redactedValue), true
	case len(body) == 0:
		return attribute.Value{}, false
	}
	if mask != nil {
		body = callMaskCallback(option, mask, span, body)
		switch {
		case len(body) == 0:
			return attribute.StringValue(redactedValue), true
		case len(body) > maxBodySize:
			return attribute.StringValue(string(bodyTooLarge)), true
		}
	}
	if redacted, ok := red.redactJSON(body); ok {
		return attribute.StringValue(redacted), true
	}
	if utf8.Valid(body) {
		return attribute.StringValue(string(body)), true
	}
	return attribute.ByteSliceValue(body), true
}

var errBodyTooLarge = errors.New("body too large")

// decompressBody decodes gzip and zlib-wrapped deflate bodies, reading at most
// maxBodySize bytes, and fails on corrupt or truncated streams.
func decompressBody(body []byte, encoding string) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip":
		reader, err = gzip.NewReader(bytes.NewReader(body))
	case "deflate":
		reader, err = zlib.NewReader(bytes.NewReader(body))
	default:
		return body, nil
	}
	if err != nil {
		return nil, err
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, maxBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > maxBodySize {
		return nil, errBodyTooLarge
	}
	return decoded, reader.Close()
}

// callMaskCallback returns nil when the callback panics, so the body is
// redacted.
func callMaskCallback(option string, mask func(sdktrace.ReadOnlySpan, []byte) []byte, span sdktrace.ReadOnlySpan, body []byte) (masked []byte) {
	defer func() {
		if p := recover(); p != nil {
			warnOnce("mask-callback-panic-"+option, "Apitally "+option+" callback panicked, bodies are replaced with "+redactedValue, "panic", p)
			masked = nil
		}
	}()
	return mask(span, body)
}

// redactJSON parses body as one JSON value and serializes it compactly,
// keeping the key order, with matching fields' string values redacted.
func (red *redaction) redactJSON(body []byte) (string, bool) {
	// encoding/json rejects a leading UTF-8 byte order mark.
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
	decoder.UseNumber()
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	token, err := decoder.Token()
	if err != nil || red.writeRedactedJSON(decoder, encoder, &out, token) != nil {
		return "", false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", false
	}
	return out.String(), true
}

func (red *redaction) writeRedactedJSON(decoder *json.Decoder, encoder *json.Encoder, out *bytes.Buffer, token json.Token) error {
	switch token {
	case json.Delim('{'):
		out.WriteByte('{')
		for i := 0; decoder.More(); i++ {
			if i > 0 {
				out.WriteByte(',')
			}
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, _ := keyToken.(string)
			if err := writeJSONScalar(encoder, out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if _, isString := valueToken.(string); isString && red.isBodyFieldRedacted(key) {
				valueToken = redactedValue
			}
			if err := red.writeRedactedJSON(decoder, encoder, out, valueToken); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case json.Delim('['):
		out.WriteByte('[')
		for i := 0; decoder.More(); i++ {
			if i > 0 {
				out.WriteByte(',')
			}
			itemToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := red.writeRedactedJSON(decoder, encoder, out, itemToken); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	default:
		return writeJSONScalar(encoder, out, token)
	}
	_, err := decoder.Token()
	return err
}

// writeJSONScalar writes a string, number, boolean or null without escaping
// HTML characters.
func writeJSONScalar(encoder *json.Encoder, out *bytes.Buffer, value any) error {
	if err := encoder.Encode(value); err != nil {
		return err
	}
	// Encode terminates each value with a newline.
	out.Truncate(out.Len() - 1)
	return nil
}
