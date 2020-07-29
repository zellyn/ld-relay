package core

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"

	"github.com/launchdarkly/ld-relay/v6/core/internal/browser"
	"github.com/launchdarkly/ld-relay/v6/core/internal/events"
	"github.com/launchdarkly/ld-relay/v6/core/internal/util"
	"github.com/launchdarkly/ld-relay/v6/core/middleware"
)

func getEventsImage(w http.ResponseWriter, req *http.Request) {
	clientCtx := middleware.GetEnvContextInfo(req.Context())

	if clientCtx.Env.GetEventDispatcher() == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(util.ErrorJSONMsg("Event proxy is not enabled for this environment"))
		return
	}
	handler := clientCtx.Env.GetEventDispatcher().GetHandler(events.JavaScriptSDKEventsEndpoint)
	if handler == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(util.ErrorJSONMsg("Event proxy for browser clients is not enabled for this environment"))
		return
	}

	d := req.URL.Query().Get("d")
	if d != "" {
		go func() {
			nullW := httptest.NewRecorder()
			eventData, _ := base64.StdEncoding.DecodeString(d)
			eventsReq, _ := http.NewRequest("POST", "", bytes.NewBuffer(eventData))
			eventsReq.Header.Add("Content-Type", "application/json")
			eventsReq.Header.Add("X-LaunchDarkly-User-Agent", eventsReq.Header.Get("X-LaunchDarkly-User-Agent"))
			eventsReq.Header.Add(events.EventSchemaHeader, strconv.Itoa(events.SummaryEventsSchemaVersion))
			handler(nullW, eventsReq)
		}()
	}

	w.Header().Set("Content-Type", "image/gif")
	_, _ = w.Write(browser.Transparent1PixelImageData)
}

func getGoals(w http.ResponseWriter, req *http.Request) {
	clientCtx := middleware.GetEnvContextInfo(req.Context())
	clientCtx.Env.GetJSClientContext().Proxy.ServeHTTP(w, req)
}
