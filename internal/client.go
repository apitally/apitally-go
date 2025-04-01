package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"regexp"
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
)

type syncPayload struct {
	Timestamp        float64                    `json:"timestamp"`
	InstanceUuid     string                     `json:"instance_uuid"`
	MessageUuid      string                     `json:"message_uuid"`
	Requests         []RequestsItem             `json:"requests"`
	ValidationErrors []ValidationErrorsItem     `json:"validation_errors,omitempty"`
	ServerErrors     []ServerErrorsItem         `json:"server_errors,omitempty"`
	Consumers        []*common.ApitallyConsumer `json:"consumers,omitempty"`
}

type PathInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type startupPayload struct {
	InstanceUuid string            `json:"instance_uuid"`
	MessageUuid  string            `json:"message_uuid"`
	Paths        []PathInfo        `json:"paths"`
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
	clientId        string
	env             string
	instanceUuid    string
	syncDataQueue   []syncPayload
	syncTicker      *time.Ticker
	startupData     *startupPayload
	startupDataSent bool
	enabled         bool
	mutex           sync.Mutex
	httpClient      *retryablehttp.Client
	done            chan struct{}

	RequestCounter         *RequestCounter
	RequestLogger          *RequestLogger
	ValidationErrorCounter *ValidationErrorCounter
	ServerErrorCounter     *ServerErrorCounter
	ConsumerRegistry       *ConsumerRegistry
}

func NewApitallyClient(config common.ApitallyConfig) (*ApitallyClient, error) {
	if !isValidClientID(config.ClientID) {
		return nil, fmt.Errorf("invalid Apitally client ID '%s' (expecting hexadecimal UUID format)", config.ClientID)
	}

	env := "dev"
	if config.Env != nil {
		env = *config.Env
	}
	if !isValidEnv(env) {
		return nil, fmt.Errorf("invalid env '%s' (expecting 1-32 alphanumeric lowercase characters and hyphens only)", env)
	}

	client := &ApitallyClient{
		clientId:      config.ClientID,
		env:           env,
		instanceUuid:  uuid.New().String(),
		syncDataQueue: make([]syncPayload, 0),
		enabled:       true,
		httpClient:    getHttpClient(),
		done:          make(chan struct{}),
	}

	client.RequestCounter = NewRequestCounter()
	client.ValidationErrorCounter = NewValidationErrorCounter()
	client.ServerErrorCounter = NewServerErrorCounter()
	client.ConsumerRegistry = NewConsumerRegistry()
	client.RequestLogger = NewRequestLogger(config.RequestLoggingConfig)

	client.startSync()
	return client, nil
}

func (c *ApitallyClient) IsEnabled() bool {
	return c.enabled
}

func (c *ApitallyClient) SetStartupData(paths []PathInfo, versions map[string]string, client string) {
	c.startupData = &startupPayload{
		InstanceUuid: c.instanceUuid,
		MessageUuid:  uuid.New().String(),
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
	url := fmt.Sprintf("%s/v2/%s/%s/%s", baseURL, c.clientId, c.env, endpoint)
	if query != "" {
		url += "?" + query
	}
	return url
}

func (c *ApitallyClient) sync() {
	c.sendSyncData()
	c.sendLogData()
	if !c.startupDataSent && c.startupData != nil {
		c.sendStartupData()
	}
}

func (c *ApitallyClient) startSync() {
	go func() {
		// Initial sync
		c.sync()

		// Use initial sync interval for the first hour
		c.syncTicker = time.NewTicker(initialSyncInterval)
		defer c.syncTicker.Stop()

		timer := time.NewTimer(initialSyncIntervalDuration)
		defer timer.Stop()

		for {
			select {
			case <-c.syncTicker.C:
				c.sync()
			case <-timer.C:
				// Switch to regular sync interval
				c.syncTicker.Stop()
				c.syncTicker = time.NewTicker(syncInterval)
			case <-c.done:
				return
			}
		}
	}()
}

func (c *ApitallyClient) stopSync() {
	if c.syncTicker != nil {
		c.syncTicker.Stop()
	}
	close(c.done)
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
	if c.startupData == nil {
		return nil
	}

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
	c.mutex.Lock()
	newPayload := syncPayload{
		Timestamp:        float64(time.Now().Unix()),
		InstanceUuid:     c.instanceUuid,
		MessageUuid:      uuid.New().String(),
		Requests:         c.RequestCounter.GetAndResetRequests(),
		ValidationErrors: c.ValidationErrorCounter.GetAndResetValidationErrors(),
		ServerErrors:     c.ServerErrorCounter.GetAndResetServerErrors(),
		Consumers:        c.ConsumerRegistry.GetAndResetUpdatedConsumers(),
	}
	c.syncDataQueue = append(c.syncDataQueue, newPayload)
	c.mutex.Unlock()

	for i := 0; len(c.syncDataQueue) > 0; i++ {
		c.mutex.Lock()
		payload := c.syncDataQueue[0]
		c.syncDataQueue = c.syncDataQueue[1:]
		c.mutex.Unlock()

		if time.Since(time.Unix(int64(payload.Timestamp), 0)) > maxQueueTime {
			continue
		}

		if i > 0 {
			c.randomDelay()
		}

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
			c.mutex.Lock()
			c.syncDataQueue = append([]syncPayload{payload}, c.syncDataQueue...)
			c.mutex.Unlock()
			break
		}
	}

	return nil
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
			suspendTime := time.Now().Add(time.Hour)
			c.RequestLogger.suspendUntil = &suspendTime
			c.RequestLogger.Clear()
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
		return HubRequestStatusRetryableError
	}

	resp, err := c.httpClient.Do(retryReq)
	if err != nil {
		return HubRequestStatusRetryableError
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusNotFound:
			fmt.Printf("Invalid Apitally client ID: '%s'\n", c.clientId)
			c.enabled = false
			c.stopSync()
			return HubRequestStatusInvalidClientId
		case http.StatusUnprocessableEntity:
			return HubRequestStatusValidationError
		case http.StatusPaymentRequired:
			return HubRequestStatusPaymentRequired
		default:
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

func isValidClientID(clientID string) bool {
	_, err := uuid.Parse(clientID)
	return err == nil
}

func isValidEnv(env string) bool {
	matched, _ := regexp.MatchString(`^[a-z0-9-]{1,32}$`, env)
	return matched
}
