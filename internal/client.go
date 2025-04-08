package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/apitally/apitally-go/common"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
)

const (
	syncInterval                = 60 * time.Second
	initialSyncInterval         = 10 * time.Second
	initialSyncIntervalDuration = time.Hour
	maxQueueTime                = time.Hour
	maxQueueSize                = 400
)

type SyncPayload struct {
	Timestamp        float64                    `json:"timestamp"`
	InstanceUUID     string                     `json:"instance_uuid"`
	MessageUUID      string                     `json:"message_uuid"`
	Requests         []RequestsItem             `json:"requests"`
	ValidationErrors []ValidationErrorsItem     `json:"validation_errors,omitempty"`
	ServerErrors     []ServerErrorsItem         `json:"server_errors,omitempty"`
	Consumers        []*common.ApitallyConsumer `json:"consumers,omitempty"`
}

type StartupPayload struct {
	InstanceUUID string            `json:"instance_uuid"`
	MessageUUID  string            `json:"message_uuid"`
	Paths        []common.PathInfo `json:"paths"`
	Versions     map[string]string `json:"versions"`
	Client       string            `json:"client"`
}

type HubRequestStatus int

const (
	HubRequestStatusOK HubRequestStatus = iota
	HubRequestStatusValidationError
	HubRequestStatusInvalidClientId
	HubRequestStatusPaymentRequired
	HubRequestStatusRetryableError
)

type ApitallyClient struct {
	enabled         bool
	instanceUUID    string
	httpClient      *retryablehttp.Client
	syncDataChan    chan SyncPayload
	syncStopped     bool
	startupData     *StartupPayload
	startupDataSent bool
	logger          *slog.Logger
	done            chan struct{}
	mutex           sync.Mutex

	Config                 common.ApitallyConfig
	RequestCounter         *RequestCounter
	RequestLogger          *RequestLogger
	ValidationErrorCounter *ValidationErrorCounter
	ServerErrorCounter     *ServerErrorCounter
	ConsumerRegistry       *ConsumerRegistry
}

func NewApitallyClient(config common.ApitallyConfig) (*ApitallyClient, error) {
	return NewApitallyClientWithHTTPClient(config, nil)
}

func NewApitallyClientWithHTTPClient(config common.ApitallyConfig, httpClient *retryablehttp.Client) (*ApitallyClient, error) {
	if !isValidClientId(config.ClientId) {
		return nil, fmt.Errorf("invalid Apitally client ID '%s' (expecting hexadecimal UUID format)", config.ClientId)
	}
	if !isValidEnv(config.Env) {
		return nil, fmt.Errorf("invalid env '%s' (expecting 1-32 alphanumeric lowercase characters and hyphens only)", config.Env)
	}

	logLevel := slog.LevelInfo
	if parseBoolEnv("APITALLY_DEBUG") {
		logLevel = slog.LevelDebug
	}
	loggerOpts := &slog.HandlerOptions{
		Level: logLevel,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, loggerOpts))

	if httpClient == nil {
		httpClient = getHttpClient()
	}

	client := &ApitallyClient{
		enabled:      true,
		instanceUUID: uuid.New().String(),
		httpClient:   httpClient,
		syncDataChan: make(chan SyncPayload, maxQueueSize),
		logger:       logger.With("component", "apitally"),
		done:         make(chan struct{}),
	}

	client.Config = config
	client.RequestCounter = NewRequestCounter()
	client.ValidationErrorCounter = NewValidationErrorCounter()
	client.ServerErrorCounter = NewServerErrorCounter()
	client.ConsumerRegistry = NewConsumerRegistry()
	client.RequestLogger = NewRequestLogger(config.RequestLoggingConfig)

	return client, nil
}

func (c *ApitallyClient) IsEnabled() bool {
	return c.enabled
}

func (c *ApitallyClient) SetStartupData(paths []common.PathInfo, versions map[string]string, client string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.startupData = &StartupPayload{
		InstanceUUID: c.instanceUUID,
		MessageUUID:  uuid.New().String(),
		Paths:        paths,
		Versions:     versions,
		Client:       client,
	}
	c.startupDataSent = false
}

func (c *ApitallyClient) getHubUrl(endpoint string, query string) string {
	baseURL := "https://hub.apitally.io"
	if envURL := os.Getenv("APITALLY_HUB_BASE_URL"); envURL != "" {
		baseURL = envURL
	}
	url := fmt.Sprintf("%s/v2/%s/%s/%s", baseURL, c.Config.ClientId, c.Config.Env, endpoint)
	if query != "" {
		url += "?" + query
	}
	return url
}

func (c *ApitallyClient) sync() {
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		c.sendStartupData()
	}()

	go func() {
		defer wg.Done()
		c.sendSyncData()
	}()

	go func() {
		defer wg.Done()
		c.sendLogData()
	}()

	wg.Wait()
}

func (c *ApitallyClient) StartSync() {
	c.RequestLogger.StartMaintenance()

	go func() {
		// Initial sync
		c.sync()

		// Use initial sync interval for the first hour
		ticker := time.NewTicker(initialSyncInterval)
		defer ticker.Stop()

		// Start the initialTimer for the initial sync interval
		initialTimer := time.NewTimer(initialSyncIntervalDuration)
		defer initialTimer.Stop()

		for {
			select {
			case <-ticker.C:
				c.sync()
			case <-initialTimer.C:
				// Switch to regular sync interval
				ticker.Stop()
				ticker = time.NewTicker(syncInterval)
			case <-c.done:
				return
			}
		}
	}()
}

func (c *ApitallyClient) stopSync() {
	if !c.syncStopped {
		close(c.done)
		c.syncStopped = true
	}
}

func (c *ApitallyClient) Shutdown() {
	c.enabled = false
	c.stopSync()

	c.sendSyncData()
	c.sendLogData()
	c.RequestLogger.Close()
	c.httpClient.HTTPClient.CloseIdleConnections()
}

func (c *ApitallyClient) sendStartupData() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.startupDataSent || c.startupData == nil {
		return nil
	}

	c.logger.Debug("Sending startup data to Apitally hub")
	jsonData, err := json.Marshal(c.startupData)
	if err != nil {
		return fmt.Errorf("failed to marshal startup data: %w", err)
	}

	url := c.getHubUrl("startup", "")
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	status := c.sendHubRequest(req)
	if status == HubRequestStatusOK {
		c.startupDataSent = true
		c.startupData = nil
	}

	return nil
}

func (c *ApitallyClient) sendSyncData() error {
	newPayload := SyncPayload{
		Timestamp:        float64(time.Now().Unix()),
		InstanceUUID:     c.instanceUUID,
		MessageUUID:      uuid.New().String(),
		Requests:         c.RequestCounter.GetAndResetRequests(),
		ValidationErrors: c.ValidationErrorCounter.GetAndResetValidationErrors(),
		ServerErrors:     c.ServerErrorCounter.GetAndResetServerErrors(),
		Consumers:        c.ConsumerRegistry.GetAndResetUpdatedConsumers(),
	}

	select {
	case c.syncDataChan <- newPayload:
		// Successfully queued the payload
	default:
		c.logger.Warn("Sync data channel is full, dropping payload")
		return fmt.Errorf("sync data channel is full")
	}

	// Process queued payloads
	for i := 0; ; i++ {
		var payload SyncPayload
		select {
		case payload = <-c.syncDataChan:
			// Got a payload to process
		default:
			// No more payloads in queue
			return nil
		}

		if time.Since(time.Unix(int64(payload.Timestamp), 0)) > maxQueueTime {
			continue
		}

		if i > 0 {
			c.randomDelay()
		}

		c.logger.Debug("Synchronizing data with Apitally hub")
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal sync data: %w", err)
		}

		url := c.getHubUrl("sync", "")
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		status := c.sendHubRequest(req)
		if status == HubRequestStatusRetryableError {
			// Put the payload back in the channel for retry
			select {
			case c.syncDataChan <- payload:
				// Successfully requeued
			default:
				c.logger.Warn("Failed to requeue payload for retrying, channel full")
			}
		}
	}
}

func (c *ApitallyClient) sendLogData() error {
	if c.RequestLogger == nil {
		return nil
	}

	if err := c.RequestLogger.rotateFile(); err != nil {
		return fmt.Errorf("failed to rotate log file: %w", err)
	}

	for i := 0; i < 10; i++ {
		logFile := c.RequestLogger.GetFile()
		if logFile == nil {
			break
		}

		if i > 0 {
			c.randomDelay()
		}

		c.logger.Debug("Sending request log data to Apitally hub")
		reader, err := logFile.GetReader()
		if err != nil {
			return fmt.Errorf("failed to get log file reader: %w", err)
		}
		defer reader.Close()

		url := c.getHubUrl("log", fmt.Sprintf("uuid=%s", logFile.uuid))
		req, err := http.NewRequest("POST", url, reader)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		status := c.sendHubRequest(req)
		if status == HubRequestStatusRetryableError {
			c.RequestLogger.RetryFileLater(logFile)
			break
		} else if status == HubRequestStatusPaymentRequired {
			logFile.Delete()
			c.RequestLogger.SuspendFor(time.Hour)
			break
		} else {
			logFile.Delete()
		}
	}

	return nil
}

func (c *ApitallyClient) sendHubRequest(req *http.Request) HubRequestStatus {
	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		c.logger.Error("Error creating retryable request for Apitally hub", "error", err)
		return HubRequestStatusRetryableError
	}

	resp, err := c.httpClient.Do(retryReq)
	if err != nil {
		c.logger.Warn("Error sending request to Apitally hub", "error", err)
		return HubRequestStatusRetryableError
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		switch resp.StatusCode {
		case http.StatusNotFound:
			c.logger.Error("Invalid Apitally client ID", "client_id", c.Config.ClientId)
			c.enabled = false
			c.stopSync()
			return HubRequestStatusInvalidClientId
		case http.StatusUnprocessableEntity:
			c.logger.Warn("Received validation error from Apitally hub")
			return HubRequestStatusValidationError
		case http.StatusPaymentRequired:
			return HubRequestStatusPaymentRequired
		default:
			c.logger.Warn("Received unexpected status code from Apitally hub", "status_code", resp.StatusCode)
			return HubRequestStatusRetryableError
		}
	}

	return HubRequestStatusOK
}

func getHttpClient() *retryablehttp.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.Logger = nil
	retryClient.HTTPClient.Timeout = 10 * time.Second
	retryClient.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		// Don't retry on context.Canceled or context.DeadlineExceeded
		if ctx.Err() != nil {
			return false, ctx.Err()
		}

		if resp != nil {
			// Only retry on 429 or 5xx responses
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
				return true, nil
			} else {
				return false, nil
			}
		}

		// Retry on all other errors (like connection errors)
		return err != nil, nil
	}
	return retryClient
}

func (c *ApitallyClient) randomDelay() {
	delay := time.Duration(100+rand.Float64()*400) * time.Millisecond
	time.Sleep(delay)
}

func isValidClientId(clientID string) bool {
	_, err := uuid.Parse(clientID)
	return err == nil
}

func isValidEnv(env string) bool {
	matched, _ := regexp.MatchString(`^[a-z0-9-]{1,32}$`, env)
	return matched
}

func parseBoolEnv(key string) bool {
	val := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return val == "1" || val == "true" || val == "yes" || val == "y"
}
