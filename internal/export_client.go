package internal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	exportRequestTimeout = 10 * time.Second
	exportIntervalHeader = "Apitally-Export-Interval"
	minExportInterval    = 5 * time.Second
	maxExportInterval    = 60 * time.Second
)

type exportOutcome int

const (
	exportAccepted exportOutcome = iota
	exportRetryable
	exportRejected
)

// exportTransportForTest replaces the network transport in tests that run in
// testing/synctest bubbles, where idle network connections would block fake
// time.
var exportTransportForTest http.RoundTripper

// exportClient posts spool files with a private transport, so neither
// instrumentation installed on http.DefaultTransport nor its retries apply.
type exportClient struct {
	client   *http.Client
	endpoint string
	header   http.Header
}

type exportResponse struct {
	outcome  exportOutcome
	status   int
	interval time.Duration
}

func newExportClient(s *settings) *exportClient {
	var transport http.RoundTripper = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if exportTransportForTest != nil {
		transport = exportTransportForTest
	}
	return &exportClient{
		client:   &http.Client{Transport: transport, Timeout: exportRequestTimeout},
		endpoint: s.otlpEndpoint,
		header: http.Header{
			"Authorization":    {"Bearer " + s.config.WriteToken},
			"Apitally-Env":     {s.config.Env},
			"Content-Type":     {"application/x-protobuf"},
			"Content-Encoding": {"gzip"},
			"User-Agent":       {distroName + "/" + sdkVersion},
		},
	}
}

func (c *exportClient) post(ctx context.Context, signal string, body []byte) exportResponse {
	resp, err := c.send(ctx, signal, body)
	var netErr net.Error
	if err != nil && ctx.Err() == nil && !(errors.As(err, &netErr) && netErr.Timeout()) {
		// The server may close an idle keep-alive connection mid-request.
		resp, err = c.send(ctx, signal, body)
	}
	if err != nil {
		logDebug("Apitally could not send buffered "+signal+", will retry", "error", err)
		return exportResponse{outcome: exportRetryable}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	result := exportResponse{status: resp.StatusCode}
	if seconds, err := strconv.Atoi(resp.Header.Get(exportIntervalHeader)); err == nil {
		result.interval = min(max(time.Duration(seconds)*time.Second, minExportInterval), maxExportInterval)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		result.outcome = exportAccepted
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		result.outcome = exportRetryable
	default:
		result.outcome = exportRejected
	}
	return result
}

func (c *exportClient) send(ctx context.Context, signal string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/"+signal, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = c.header.Clone()
	return c.client.Do(req)
}

func (c *exportClient) close() {
	c.client.CloseIdleConnections()
}
