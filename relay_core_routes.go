package relay

import (
	"net/http"

	"github.com/gorilla/mux"

	"gopkg.in/launchdarkly/go-sdk-common.v2/ldlog"

	"github.com/launchdarkly/ld-relay/v6/internal/events"
	"github.com/launchdarkly/ld-relay/v6/internal/logging"
	"github.com/launchdarkly/ld-relay/v6/internal/metrics"
)

const (
	serverSideStreamLogMessage          = "Application requested server-side /all stream"
	serverSideFlagsOnlyStreamLogMessage = "Application requested server-side /flags stream"
)

func (r *RelayCore) MakeRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(logging.GlobalContextLoggersMiddleware(r.loggers))
	if r.loggers.GetMinLevel() == ldlog.Debug {
		router.Use(logging.RequestLoggerMiddleware(r.loggers))
	}
	router.Handle("/status", statusHandler(r)).Methods("GET")

	sdkKeySelector := selectEnvironmentByAuthorizationKey(serverSdk, r)
	mobileKeySelector := selectEnvironmentByAuthorizationKey(mobileSdk, r)
	jsClientSelector := selectEnvironmentByEnvIDUrlParam(r)

	// Client-side evaluation
	clientSideMiddlewareStack := chainMiddleware(
		corsMiddleware,
		jsClientSelector,
		requestCountMiddleware(metrics.BrowserRequests))

	goalsRouter := router.PathPrefix("/sdk/goals").Subrouter()
	goalsRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(goalsRouter))
	goalsRouter.HandleFunc("/{envId}", getGoals).Methods("GET", "OPTIONS")

	clientSideSdkEvalRouter := router.PathPrefix("/sdk/eval/{envId}/").Subrouter()
	clientSideSdkEvalRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(clientSideSdkEvalRouter))
	clientSideSdkEvalRouter.HandleFunc("/users/{user}", evaluateAllFeatureFlagsValueOnly(jsClientSdk)).Methods("GET", "OPTIONS")
	clientSideSdkEvalRouter.HandleFunc("/user", evaluateAllFeatureFlagsValueOnly(jsClientSdk)).Methods("REPORT", "OPTIONS")

	clientSideSdkEvalXRouter := router.PathPrefix("/sdk/evalx/{envId}/").Subrouter()
	clientSideSdkEvalXRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(clientSideSdkEvalXRouter))
	clientSideSdkEvalXRouter.HandleFunc("/users/{user}", evaluateAllFeatureFlags(jsClientSdk)).Methods("GET", "OPTIONS")
	clientSideSdkEvalXRouter.HandleFunc("/user", evaluateAllFeatureFlags(jsClientSdk)).Methods("REPORT", "OPTIONS")

	serverSideMiddlewareStack := chainMiddleware(
		sdkKeySelector,
		requestCountMiddleware(metrics.ServerRequests))

	serverSideSdkRouter := router.PathPrefix("/sdk/").Subrouter()
	// (?)TODO: there is a bug in gorilla mux (see see https://github.com/gorilla/mux/pull/378) that means the middleware below
	// because it will not be run if it matches any earlier prefix.  Until it is fixed, we have to apply the middleware explicitly
	// serverSideSdkRouter.Use(serverSideMiddlewareStack)

	serverSideEvalRouter := serverSideSdkRouter.PathPrefix("/eval/").Subrouter()
	serverSideEvalRouter.Handle("/users/{user}", serverSideMiddlewareStack(http.HandlerFunc(evaluateAllFeatureFlagsValueOnly(serverSdk)))).Methods("GET")
	serverSideEvalRouter.Handle("/user", serverSideMiddlewareStack(http.HandlerFunc(evaluateAllFeatureFlagsValueOnly(serverSdk)))).Methods("REPORT")

	serverSideEvalXRouter := serverSideSdkRouter.PathPrefix("/evalx/").Subrouter()
	serverSideEvalXRouter.Handle("/users/{user}", serverSideMiddlewareStack(http.HandlerFunc(evaluateAllFeatureFlags(serverSdk)))).Methods("GET")
	serverSideEvalXRouter.Handle("/user", serverSideMiddlewareStack(http.HandlerFunc(evaluateAllFeatureFlags(serverSdk)))).Methods("REPORT")

	// PHP SDK endpoints
	serverSideSdkRouter.Handle("/flags", serverSideMiddlewareStack(http.HandlerFunc(pollAllFlagsHandler))).Methods("GET")
	serverSideSdkRouter.Handle("/flags/{key}", serverSideMiddlewareStack(http.HandlerFunc(pollFlagHandler))).Methods("GET")
	serverSideSdkRouter.Handle("/segments/{key}", serverSideMiddlewareStack(http.HandlerFunc(pollSegmentHandler))).Methods("GET")

	// Mobile evaluation
	mobileMiddlewareStack := chainMiddleware(
		mobileKeySelector,
		requestCountMiddleware(metrics.MobileRequests))

	msdkRouter := router.PathPrefix("/msdk/").Subrouter()
	msdkRouter.Use(mobileMiddlewareStack)

	msdkEvalRouter := msdkRouter.PathPrefix("/eval/").Subrouter()
	msdkEvalRouter.HandleFunc("/users/{user}", evaluateAllFeatureFlagsValueOnly(mobileSdk)).Methods("GET")
	msdkEvalRouter.HandleFunc("/user", evaluateAllFeatureFlagsValueOnly(mobileSdk)).Methods("REPORT")

	msdkEvalXRouter := msdkRouter.PathPrefix("/evalx/").Subrouter()
	msdkEvalXRouter.HandleFunc("/users/{user}", evaluateAllFeatureFlags(mobileSdk)).Methods("GET")
	msdkEvalXRouter.HandleFunc("/user", evaluateAllFeatureFlags(mobileSdk)).Methods("REPORT")

	mobileStreamRouter := router.PathPrefix("/meval").Subrouter()
	mobileStreamRouter.Use(mobileMiddlewareStack, streamingMiddleware)
	mobilePingWithUser := pingStreamHandlerWithUser(mobileSdk, r.mobileStreamProvider)
	mobileStreamRouter.Handle("", countMobileConns(mobilePingWithUser)).Methods("REPORT")
	mobileStreamRouter.Handle("/{user}", countMobileConns(mobilePingWithUser)).Methods("GET")

	router.Handle("/mping", mobileKeySelector(
		countMobileConns(streamingMiddleware(pingStreamHandler(r.mobileStreamProvider))))).Methods("GET")

	jsPing := pingStreamHandler(r.jsClientStreamProvider)
	jsPingWithUser := pingStreamHandlerWithUser(jsClientSdk, r.jsClientStreamProvider)

	clientSidePingRouter := router.PathPrefix("/ping/{envId}").Subrouter()
	clientSidePingRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(clientSidePingRouter), streamingMiddleware)
	clientSidePingRouter.Handle("", countBrowserConns(jsPing)).Methods("GET", "OPTIONS")

	clientSideStreamEvalRouter := router.PathPrefix("/eval/{envId}").Subrouter()
	clientSideStreamEvalRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(clientSideStreamEvalRouter), streamingMiddleware)
	// For now we implement eval as simply ping
	clientSideStreamEvalRouter.Handle("/{user}", countBrowserConns(jsPingWithUser)).Methods("GET", "OPTIONS")
	clientSideStreamEvalRouter.Handle("", countBrowserConns(jsPingWithUser)).Methods("REPORT", "OPTIONS")

	mobileEventsRouter := router.PathPrefix("/mobile").Subrouter()
	mobileEventsRouter.Use(mobileMiddlewareStack)
	mobileEventsRouter.Handle("/events/bulk", bulkEventHandler(events.MobileSDKEventsEndpoint)).Methods("POST")
	mobileEventsRouter.Handle("/events", bulkEventHandler(events.MobileSDKEventsEndpoint)).Methods("POST")
	mobileEventsRouter.Handle("", bulkEventHandler(events.MobileSDKEventsEndpoint)).Methods("POST")
	mobileEventsRouter.Handle("/events/diagnostic", bulkEventHandler(events.MobileSDKDiagnosticEventsEndpoint)).Methods("POST")

	clientSideBulkEventsRouter := router.PathPrefix("/events/bulk/{envId}").Subrouter()
	clientSideBulkEventsRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(clientSideBulkEventsRouter))
	clientSideBulkEventsRouter.Handle("", bulkEventHandler(events.JavaScriptSDKEventsEndpoint)).Methods("POST", "OPTIONS")

	clientSideDiagnosticEventsRouter := router.PathPrefix("/events/diagnostic/{envId}").Subrouter()
	clientSideDiagnosticEventsRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(clientSideBulkEventsRouter))
	clientSideDiagnosticEventsRouter.Handle("", bulkEventHandler(events.JavaScriptSDKDiagnosticEventsEndpoint)).Methods("POST", "OPTIONS")

	clientSideImageEventsRouter := router.PathPrefix("/a/{envId}.gif").Subrouter()
	clientSideImageEventsRouter.Use(clientSideMiddlewareStack, mux.CORSMethodMiddleware(clientSideImageEventsRouter))
	clientSideImageEventsRouter.HandleFunc("", getEventsImage).Methods("GET", "OPTIONS")

	serverSideRouter := router.PathPrefix("").Subrouter()
	serverSideRouter.Use(serverSideMiddlewareStack)
	serverSideRouter.Handle("/bulk", bulkEventHandler(events.ServerSDKEventsEndpoint)).Methods("POST")
	serverSideRouter.Handle("/diagnostic", bulkEventHandler(events.ServerSDKDiagnosticEventsEndpoint)).Methods("POST")
	serverSideRouter.Handle("/all", countServerConns(streamingMiddleware(
		streamHandler(r.serverSideStreamProvider, serverSideStreamLogMessage),
	))).Methods("GET")
	serverSideRouter.Handle("/flags", countServerConns(streamingMiddleware(
		streamHandler(r.serverSideFlagsStreamProvider, serverSideFlagsOnlyStreamLogMessage),
	))).Methods("GET")

	return router
}
