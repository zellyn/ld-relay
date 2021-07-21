package bigsegments

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/launchdarkly/go-test-helpers/v2/httphelpers"
	"github.com/launchdarkly/ld-relay/v6/config"
	"github.com/launchdarkly/ld-relay/v6/internal/core/httpconfig"
	"github.com/launchdarkly/ld-relay/v6/internal/core/sharedtest"

	"gopkg.in/launchdarkly/go-sdk-common.v2/ldlog"
	"gopkg.in/launchdarkly/go-sdk-common.v2/ldlogtest"
	"gopkg.in/launchdarkly/go-sdk-common.v2/ldtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bigSegmentStoreMock struct {
	cursor     string
	lock       sync.Mutex
	patchCh    chan bigSegmentPatch
	syncTimeCh chan ldtime.UnixMillisecondTime
}

func (s *bigSegmentStoreMock) applyPatch(patch bigSegmentPatch) (bool, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.cursor != patch.PreviousVersion {
		return false, nil
	}
	s.cursor = patch.Version

	s.patchCh <- patch

	return true, nil
}

func (s *bigSegmentStoreMock) getCursor() (string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.cursor, nil
}

func (s *bigSegmentStoreMock) setSynchronizedOn(synchronizedOn ldtime.UnixMillisecondTime) error {
	s.syncTimeCh <- synchronizedOn

	return nil
}

func (s *bigSegmentStoreMock) GetSynchronizedOn() (ldtime.UnixMillisecondTime, error) {
	return 0, nil
}

func (s *bigSegmentStoreMock) Close() error {
	return nil
}

func newBigSegmentStoreMock() *bigSegmentStoreMock {
	return &bigSegmentStoreMock{
		patchCh:    make(chan bigSegmentPatch, 100),
		syncTimeCh: make(chan ldtime.UnixMillisecondTime, 100),
	}
}

func assertPollRequest(t *testing.T, req httphelpers.HTTPRequestInfo, afterVersion string) {
	assert.Equal(t, string(testSDKKey), req.Request.Header.Get("Authorization"))
	assert.Equal(t, unboundedPollPath, req.Request.URL.Path)
	if afterVersion == "" {
		assert.Equal(t, "", req.Request.URL.RawQuery)
	} else {
		assert.Equal(t, "after="+afterVersion, req.Request.URL.RawQuery)
	}
}

func assertStreamRequest(t *testing.T, req httphelpers.HTTPRequestInfo) {
	assert.Equal(t, string(testSDKKey), req.Request.Header.Get("Authorization"))
	assert.Equal(t, unboundedStreamPath, req.Request.URL.Path)
}

func requirePatch(t *testing.T, s *bigSegmentStoreMock, expectedPatch bigSegmentPatch) {
	select {
	case patch := <-s.patchCh:
		require.Equal(t, expectedPatch, patch)
	case <-time.After(time.Second):
		require.Fail(t, "timed out waiting for patch")
	}
}

func TestBasicSync(t *testing.T) {
	mockLog := ldlogtest.NewMockLog()
	mockLog.Loggers.SetMinLevel(ldlog.Debug)
	defer mockLog.DumpIfTestFailed(t)

	patch1 := newPatchBuilder("segment.g1", "1", "").
		addIncludes("included1", "included2").addExcludes("excluded1", "excluded2").build()
	patch2 := newPatchBuilder("segment.g1", "2", "1").
		removeIncludes("included1").removeExcludes("excluded1").build()

	pollHandler, requestsCh := httphelpers.RecordingHandler(
		httphelpers.SequentialHandler(
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{patch1}, nil),
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{}, nil),
		),
	)

	sseHandler, _ := httphelpers.SSEHandler(makePatchEvent(patch2))
	streamHandler, streamRequestsCh := httphelpers.RecordingHandler(sseHandler)

	httphelpers.WithServer(pollHandler, func(pollServer *httptest.Server) {
		httphelpers.WithServer(streamHandler, func(streamServer *httptest.Server) {
			startTime := ldtime.UnixMillisNow()

			storeMock := newBigSegmentStoreMock()
			defer storeMock.Close()

			httpConfig, err := httpconfig.NewHTTPConfig(config.ProxyConfig{}, nil, "", mockLog.Loggers)
			require.NoError(t, err)

			segmentSync := newDefaultBigSegmentSynchronizer(httpConfig, storeMock,
				pollServer.URL, streamServer.URL, config.EnvironmentID("env-xyz"), testSDKKey, mockLog.Loggers)
			defer segmentSync.Close()
			segmentSync.Start()

			pollReq1 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq1, "")
			requirePatch(t, storeMock, patch1)
			require.Equal(t, 0, len(storeMock.syncTimeCh))

			pollReq2 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq2, patch1.Version)

			pollReq3 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq3, patch1.Version)

			require.Equal(t, 0, len(storeMock.patchCh))

			sharedtest.ExpectNoTestRequests(t, requestsCh, time.Millisecond*50)

			syncTime := <-storeMock.syncTimeCh
			assert.True(t, syncTime >= startTime)
			assert.True(t, syncTime <= ldtime.UnixMillisNow())

			streamReq1 := sharedtest.ExpectTestRequest(t, streamRequestsCh, time.Second)
			assertStreamRequest(t, streamReq1)
			requirePatch(t, storeMock, patch2)

			sharedtest.ExpectNoTestRequests(t, streamRequestsCh, time.Millisecond*50)

			assert.Equal(t, []string{
				"BigSegmentSynchronizer: Applied 1 update",
				"BigSegmentSynchronizer: Applied 1 update",
			}, mockLog.GetOutput(ldlog.Info))
			assert.Len(t, mockLog.GetOutput(ldlog.Warn), 0)
		})
	})
}

func TestSyncSkipsOutOfOrderUpdateFromPoll(t *testing.T) {
	// Scenario:
	// - Poll returns 3 patches: first patch is valid, second patch is non-matching, third is matching
	// - We apply the first patch
	// - Second patch causes a warning and causes remainder of list to be skipped
	// - Then we proceed with stream request as usual
	mockLog := ldlogtest.NewMockLog()
	mockLog.Loggers.SetMinLevel(ldlog.Debug)
	defer mockLog.DumpIfTestFailed(t)

	patch1 := newPatchBuilder("segment.g1", "1", "").
		addIncludes("included1", "included2").addExcludes("excluded1", "excluded2").build()
	patch1x := newPatchBuilder("segment.g1", "1x", "non-matching-previous-version").
		addIncludes("includedx").addExcludes("excludedx").build()
	patch1y := newPatchBuilder("segment.g1", "2", "1").
		addIncludes("includedy").addExcludes("excludedy").build()
	patch2 := newPatchBuilder("segment.g1", "2", "1").
		removeIncludes("included1").removeExcludes("excluded1").build()

	pollHandler, requestsCh := httphelpers.RecordingHandler(
		httphelpers.SequentialHandler(
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{patch1, patch1x, patch1y}, nil),
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{}, nil),
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{}, nil),
		),
	)

	sseHandler, _ := httphelpers.SSEHandler(makePatchEvent(patch2))
	streamHandler, streamRequestsCh := httphelpers.RecordingHandler(sseHandler)

	httphelpers.WithServer(pollHandler, func(pollServer *httptest.Server) {
		httphelpers.WithServer(streamHandler, func(streamServer *httptest.Server) {
			startTime := ldtime.UnixMillisNow()

			storeMock := newBigSegmentStoreMock()
			defer storeMock.Close()

			httpConfig, err := httpconfig.NewHTTPConfig(config.ProxyConfig{}, nil, "", mockLog.Loggers)
			require.NoError(t, err)

			segmentSync := newDefaultBigSegmentSynchronizer(httpConfig, storeMock,
				pollServer.URL, streamServer.URL, config.EnvironmentID("env-xyz"), testSDKKey, mockLog.Loggers)
			defer segmentSync.Close()
			segmentSync.Start()

			pollReq1 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq1, "")
			requirePatch(t, storeMock, patch1)
			require.Equal(t, 0, len(storeMock.syncTimeCh))

			pollReq2 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq2, patch1.Version)

			pollReq3 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq3, patch1.Version)

			require.Equal(t, 0, len(storeMock.patchCh))
			sharedtest.ExpectNoTestRequests(t, requestsCh, time.Millisecond*50)

			syncTime := <-storeMock.syncTimeCh
			assert.True(t, syncTime >= startTime)
			assert.True(t, syncTime <= ldtime.UnixMillisNow())

			streamReq1 := sharedtest.ExpectTestRequest(t, streamRequestsCh, time.Second)
			assertStreamRequest(t, streamReq1)
			requirePatch(t, storeMock, patch2)

			sharedtest.ExpectNoTestRequests(t, streamRequestsCh, time.Millisecond*50)

			assert.Equal(t, []string{
				"BigSegmentSynchronizer: Applied 1 update",
				"BigSegmentSynchronizer: Applied 1 update",
			}, mockLog.GetOutput(ldlog.Info))
			mockLog.AssertMessageMatch(t, true, ldlog.Warn, `"non-matching-previous-version" which was not the latest`)
		})
	})
}

func TestSyncSkipsOutOfOrderUpdateFromStreamAndRestartsStream(t *testing.T) {
	mockLog := ldlogtest.NewMockLog()
	mockLog.Loggers.SetMinLevel(ldlog.Debug)
	defer mockLog.DumpIfTestFailed(t)

	patch1 := newPatchBuilder("segment.g1", "1", "").
		addIncludes("included1", "included2").addExcludes("excluded1", "excluded2").build()
	patch2x := newPatchBuilder("segment.g1", "2", "non-matching-previous-version").
		removeIncludes("included1").removeExcludes("excluded1").build()
	patch2 := newPatchBuilder("segment.g1", "2", "1").
		removeIncludes("included1").removeExcludes("excluded1").build()

	pollHandler, requestsCh := httphelpers.RecordingHandler(
		httphelpers.SequentialHandler(
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{patch1}, nil),
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{}, nil),
			httphelpers.HandlerWithJSONResponse([]bigSegmentPatch{}, nil),
		),
	)

	firstStream, _ := httphelpers.SSEHandler(makePatchEvent(patch2x))
	secondStream, _ := httphelpers.SSEHandler(makePatchEvent(patch2))
	streamHandler, streamRequestsCh := httphelpers.RecordingHandler(
		httphelpers.SequentialHandler(firstStream, secondStream),
	)

	httphelpers.WithServer(pollHandler, func(pollServer *httptest.Server) {
		httphelpers.WithServer(streamHandler, func(streamServer *httptest.Server) {
			startTime := ldtime.UnixMillisNow()

			storeMock := newBigSegmentStoreMock()
			defer storeMock.Close()

			httpConfig, err := httpconfig.NewHTTPConfig(config.ProxyConfig{}, nil, "", mockLog.Loggers)
			require.NoError(t, err)

			segmentSync := newDefaultBigSegmentSynchronizer(httpConfig, storeMock,
				pollServer.URL, streamServer.URL, config.EnvironmentID("env-xyz"), testSDKKey, mockLog.Loggers)
			segmentSync.streamRetryInterval = time.Millisecond
			defer segmentSync.Close()
			segmentSync.Start()

			pollReq1 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq1, "")
			requirePatch(t, storeMock, patch1)
			require.Equal(t, 0, len(storeMock.syncTimeCh))

			pollReq2 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq2, patch1.Version)

			pollReq3 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq3, patch1.Version)

			require.Equal(t, 0, len(storeMock.patchCh))

			syncTime := <-storeMock.syncTimeCh
			assert.True(t, syncTime >= startTime)
			assert.True(t, syncTime <= ldtime.UnixMillisNow())

			streamReq1 := sharedtest.ExpectTestRequest(t, streamRequestsCh, time.Second)
			assertStreamRequest(t, streamReq1)

			pollReq4 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq4, patch1.Version)

			streamReq2 := sharedtest.ExpectTestRequest(t, streamRequestsCh, time.Second)
			assertStreamRequest(t, streamReq2)

			pollReq5 := sharedtest.ExpectTestRequest(t, requestsCh, time.Second)
			assertPollRequest(t, pollReq5, patch1.Version)
			sharedtest.ExpectNoTestRequests(t, requestsCh, time.Millisecond*50)

			requirePatch(t, storeMock, patch2)

			sharedtest.ExpectNoTestRequests(t, streamRequestsCh, time.Millisecond*50)

			assert.Equal(t, []string{
				"BigSegmentSynchronizer: Applied 1 update",
				"BigSegmentSynchronizer: Applied 1 update",
			}, mockLog.GetOutput(ldlog.Info))
			mockLog.AssertMessageMatch(t, true, ldlog.Warn, `"non-matching-previous-version" which was not the latest`)
		})
	})
}
