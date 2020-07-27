package relayenv

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/launchdarkly/go-server-sdk.v5/interfaces/ldstoretypes"

	"github.com/launchdarkly/ld-relay/v6/config"
	"github.com/launchdarkly/ld-relay/v6/internal/events"
	"github.com/launchdarkly/ld-relay/v6/internal/httpconfig"
	"github.com/launchdarkly/ld-relay/v6/internal/metrics"
	"github.com/launchdarkly/ld-relay/v6/internal/store"
	"github.com/launchdarkly/ld-relay/v6/internal/streams"
	"github.com/launchdarkly/ld-relay/v6/internal/util"
	"github.com/launchdarkly/ld-relay/v6/sdkconfig"
	"gopkg.in/launchdarkly/go-sdk-common.v2/ldlog"
	ld "gopkg.in/launchdarkly/go-server-sdk.v5"
	"gopkg.in/launchdarkly/go-server-sdk.v5/interfaces"
	"gopkg.in/launchdarkly/go-server-sdk.v5/ldcomponents"
)

type envContextImpl struct {
	mu             sync.RWMutex
	client         sdkconfig.LDClientContext
	storeAdapter   *store.SSERelayDataStoreAdapter
	loggers        ldlog.Loggers
	handlers       ClientHandlers
	credentials    Credentials
	name           string
	secureMode     bool
	publishers     *streams.Publishers
	envStreams     *streams.EnvStreams
	metricsManager *metrics.Manager
	metricsEnv     *metrics.EnvironmentManager
	ttl            time.Duration
	initErr        error
}

type envContextStoreQueries struct {
	context *envContextImpl
}

// NewEnvContext creates the internal implementation of EnvContext.
//
// It immediately begins trying to initialize the SDK client for this environment. Since that might
// take a while, it is done on a separate goroutine. The EnvContext instance is returned immediately
// in an uninitialized state, and once the SDK client initialization has either succeeded or failed,
// the same EnvContext will be pushed to the channel readyCh.
//
// NewEnvContext can also immediately return an error, with a nil EnvContext, if the configuration is
// invalid.
func NewEnvContext(
	envName string,
	envConfig config.EnvConfig,
	allConfig config.Config,
	clientFactory sdkconfig.ClientFactoryFunc,
	dataStoreFactory interfaces.DataStoreFactory,
	publishers *streams.Publishers,
	metricsManager *metrics.Manager,
	loggers ldlog.Loggers,
	readyCh chan<- EnvContext,
) (EnvContext, error) {
	var thingsToCleanUp util.CleanupTasks // keeps track of partially constructed things in case we exit early
	defer thingsToCleanUp.Run()

	envLoggers := loggers
	envLoggers.SetPrefix(makeLogPrefix(envName))
	envLoggers.SetMinLevel(
		envConfig.LogLevel.GetOrElse(
			allConfig.Main.LogLevel.GetOrElse(ldlog.Info),
		),
	)

	httpConfig, err := httpconfig.NewHTTPConfig(allConfig.Proxy, envConfig.SDKKey, loggers)
	if err != nil {
		return nil, err
	}

	envContext := &envContextImpl{
		name: envName,
		credentials: Credentials{
			SDKKey:        envConfig.SDKKey,
			MobileKey:     envConfig.MobileKey,
			EnvironmentID: envConfig.EnvID,
		},
		loggers:        envLoggers,
		secureMode:     envConfig.SecureMode,
		publishers:     publishers,
		metricsManager: metricsManager,
		ttl:            envConfig.TTL.GetOrElse(0),
	}

	envStreams := streams.NewEnvStreams(
		publishers,
		envContextStoreQueries{envContext},
		envConfig.SDKKey,
		envConfig.MobileKey,
		envConfig.EnvID,
		allConfig.Main.HeartbeatInterval.GetOrElse(config.DefaultHeartbeatInterval),
		envLoggers,
	)
	envContext.envStreams = envStreams
	thingsToCleanUp.AddCloser(envStreams)

	storeAdapter := store.NewSSERelayDataStoreAdapter(dataStoreFactory, envStreams)
	envContext.storeAdapter = storeAdapter

	var eventDispatcher *events.EventDispatcher
	if allConfig.Events.SendEvents {
		envLoggers.Info("Proxying events for this environment")
		eventDispatcher = events.NewEventDispatcher(envConfig.SDKKey, envConfig.MobileKey, envConfig.EnvID,
			envLoggers, allConfig.Events, httpConfig, storeAdapter)
	}

	envContext.handlers = ClientHandlers{
		EventDispatcher:       eventDispatcher,
		AllStreamHandler:      publishers.ServerSideAll.Handler(string(envConfig.SDKKey)),
		FlagsStreamHandler:    publishers.ServerSideFlags.Handler(string(envConfig.SDKKey)),
		MobileStreamHandler:   publishers.Mobile.Handler(string(envConfig.MobileKey)),
		JSClientStreamHandler: publishers.JSClient.Handler(string(envConfig.EnvID)),
	}

	eventsURI := allConfig.Events.EventsURI.String()
	if eventsURI == "" {
		eventsURI = config.DefaultEventsURI
	}
	eventsPublisher, err := events.NewHttpEventPublisher(envConfig.SDKKey, envLoggers,
		events.OptionUri(eventsURI),
		events.OptionClient{Client: httpConfig.Client()})
	if err != nil {
		return nil, fmt.Errorf("unable to create publisher: %w", err)
	}

	var em *metrics.EnvironmentManager
	if metricsManager != nil {
		em, err = metricsManager.AddEnvironment(envName, eventsPublisher)
		if err != nil {
			return nil, fmt.Errorf("unable to create metrics processor: %w", err)
		}
	}
	envContext.metricsEnv = em
	thingsToCleanUp.AddFunc(func() { metricsManager.RemoveEnvironment(em) })

	clientConfig := ld.Config{
		DataSource: ldcomponents.StreamingDataSource().BaseURI(allConfig.Main.StreamURI.String()),
		DataStore:  storeAdapter,
		HTTP:       httpConfig.SDKHTTPConfigFactory,
		Logging:    ldcomponents.Logging().Loggers(envLoggers),
	}

	// Connecting may take time, so do this in parallel
	go func(envName string, envConfig config.EnvConfig) {
		client, err := clientFactory(envConfig.SDKKey, clientConfig)
		envContext.SetClient(client)

		if err != nil {
			envContext.initErr = err
			if !allConfig.Main.IgnoreConnectionErrors {
				envLoggers.Errorf("Error initializing LaunchDarkly client for %s: %+v\n", envName, err)

				if allConfig.Main.ExitOnError {
					os.Exit(1)
				}
				if readyCh != nil {
					readyCh <- envContext
				}
				return
			}

			loggers.Errorf("Ignoring error initializing LaunchDarkly client for %s: %+v\n", envName, err)
		} else {
			loggers.Infof("Initialized LaunchDarkly client for %s\n", envName)
		}
		if readyCh != nil {
			readyCh <- envContext
		}
	}(envName, envConfig)

	thingsToCleanUp.Clear()

	return envContext, nil
}

func (c *envContextImpl) GetName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.name
}

func (c *envContextImpl) GetCredentials() Credentials {
	return c.credentials
}

func (c *envContextImpl) GetClient() sdkconfig.LDClientContext {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

func (c *envContextImpl) SetClient(client sdkconfig.LDClientContext) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client = client
}

func (c *envContextImpl) GetStore() interfaces.DataStore {
	if c.storeAdapter == nil {
		return nil
	}
	return c.storeAdapter.GetStore()
}

func (c *envContextImpl) GetLoggers() ldlog.Loggers {
	return c.loggers
}

func (c *envContextImpl) GetHandlers() ClientHandlers {
	return c.handlers
}

func (c *envContextImpl) GetMetricsContext() context.Context {
	if c.metricsEnv == nil {
		return context.Background()
	}
	return c.metricsEnv.GetOpenCensusContext()
}

func (c *envContextImpl) GetTTL() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.ttl
}

func (c *envContextImpl) GetInitError() error {
	return c.initErr
}

func (c *envContextImpl) IsSecureMode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.secureMode
}

func (c *envContextImpl) Close() error {
	_ = c.envStreams.Close()
	if c.metricsManager != nil && c.metricsEnv != nil {
		c.metricsManager.RemoveEnvironment(c.metricsEnv)
	}
	return nil
}

func (q envContextStoreQueries) IsInitialized() bool {
	if s := q.context.storeAdapter.GetStore(); s != nil {
		return s.IsInitialized()
	}
	return false
}

func (q envContextStoreQueries) GetAll(kind ldstoretypes.DataKind) ([]ldstoretypes.KeyedItemDescriptor, error) {
	if s := q.context.storeAdapter.GetStore(); s != nil {
		return s.GetAll(kind)
	}
	return nil, nil
}

func makeLogPrefix(envName string) string {
	return fmt.Sprintf("[env: %s]", envName)
}
