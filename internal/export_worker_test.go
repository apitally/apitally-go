package internal

import (
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apitally/apitally-go/internal/testutils"
)

func TestRetriedFileIsSentByteIdentically(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	synctest.Test(t, func(t *testing.T) {
		startRuntimeForTest(t, server, nil)
		server.SetResponse(http.StatusServiceUnavailable, 0)

		time.Sleep(initialExportDelay + time.Second)
		server.SetResponse(http.StatusOK, 0)
		time.Sleep(defaultExportInterval * 11 / 10)
		synctest.Wait()

		requests := server.Requests()
		require.GreaterOrEqual(t, len(requests), 2)
		assert.Equal(t, http.StatusServiceUnavailable, requests[0].Status)
		assert.Equal(t, signalLogs, requests[0].Signal)
		assert.Equal(t, signalLogs, requests[1].Signal)
		assert.Equal(t, requests[0].Body, requests[1].Body)
	})
}

func TestRetryableFailureEndsCycleAndRejectedFileIsDropped(t *testing.T) {
	for _, tc := range []struct {
		status               int
		requestsInFirstCycle int
		isResent             bool
	}{
		{http.StatusServiceUnavailable, 1, true},
		{http.StatusTooManyRequests, 1, true},
		{http.StatusUnauthorized, 2, false},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			logs := testutils.RecordSlog(t)
			synctest.Test(t, func(t *testing.T) {
				startRuntimeForTest(t, server, nil)
				server.SetResponse(tc.status, 0)

				time.Sleep(initialExportDelay + time.Second)
				firstCycle := server.Requests()
				server.SetResponse(http.StatusOK, 0)
				time.Sleep(defaultExportInterval * 11 / 10)
				synctest.Wait()

				require.Len(t, firstCycle, tc.requestsInFirstCycle)
				isResent := false
				for _, req := range server.Requests()[len(firstCycle):] {
					isResent = isResent || string(req.Body) == string(firstCycle[0].Body)
				}
				assert.Equal(t, tc.isResent, isResent)
				assert.Equal(t, !tc.isResent, slices.ContainsFunc(logs.Messages(slog.LevelWarn), func(msg string) bool {
					return strings.Contains(msg, "rejected")
				}))
			})
		})
	}
}

func TestExportIntervalHeaderIsClampedToRange(t *testing.T) {
	for _, tc := range []struct {
		headerSeconds int
		interval      time.Duration
	}{
		{1, minExportInterval},
		{120, maxExportInterval},
	} {
		t.Run(strconv.Itoa(tc.headerSeconds), func(t *testing.T) {
			server := testutils.NewOTLPServer(t)
			synctest.Test(t, func(t *testing.T) {
				startRuntimeForTest(t, server, nil)
				server.SetResponse(http.StatusOK, tc.headerSeconds)

				time.Sleep(initialExportDelay + time.Second)
				firstCycle := server.Requests()
				time.Sleep(tc.interval * 11 / 10)
				synctest.Wait()

				requests := server.Requests()
				require.Greater(t, len(requests), len(firstCycle))
				gap := requests[len(firstCycle)].Time.Sub(firstCycle[len(firstCycle)-1].Time)
				assert.InDelta(t, tc.interval, gap, float64(tc.interval)/10)
			})
		})
	}
}

func TestSendBudgetLimitsBacklogFilesPerCycle(t *testing.T) {
	server := testutils.NewOTLPServer(t)
	synctest.Test(t, func(t *testing.T) {
		startRuntimeForTest(t, server, nil)
		spool := currentRuntime.Load().spool
		for range 15 {
			spool.append(signalTraces, []byte("backlog"))
			spool.closeCurrentFiles()
		}
		server.SetResponse(http.StatusServiceUnavailable, 0)
		time.Sleep(initialExportDelay + time.Millisecond)
		synctest.Wait()
		require.Len(t, server.Requests(), 1)

		server.SetResponse(http.StatusOK, 0)
		time.Sleep(defaultExportInterval*11/10 + 5*time.Second)
		synctest.Wait()

		assert.Len(t, server.Requests(), 1+maxBacklogSendsPerCycle)
	})
}
