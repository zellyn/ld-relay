package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/launchdarkly/go-sdk-common.v2/ldlog"
)

func TestConfigFromEnvironmentWithValidProperties(t *testing.T) {
	for _, tdc := range makeValidConfigs() {
		t.Run(tdc.name, func(t *testing.T) {
			testValidConfigVars(t, tdc.makeConfig, tdc.envVars)
		})
	}
}

func TestConfigFromEnvironmentWithInvalidProperties(t *testing.T) {
	for _, tdc := range makeInvalidConfigs() {
		t.Run(tdc.name, func(t *testing.T) {
			testInvalidConfigVars(t, tdc.envVars, tdc.envVarsError)
		})
	}
}

func TestConfigFromEnvironmentDeprecatedUsage(t *testing.T) {
	t.Run("HEARTBEAT_INTERVAL as number of seconds", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) { c.Main.HeartbeatInterval = NewOptDuration(3 * time.Second) },
			map[string]string{"HEARTBEAT_INTERVAL": "3"},
		)
	})

	t.Run("EVENTS_FLUSH_INTERVAL as number of seconds", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) { c.Events.FlushInterval = NewOptDuration(3 * time.Second) },
			map[string]string{"EVENTS_FLUSH_INTERVAL": "3"}, // this property was formerly defined as seconds
		)
	})

	t.Run("LD_TTL_MINUTES_env", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) {
				c.Environment = make(map[string]*EnvConfig)
				c.Environment["envname"] = &EnvConfig{
					SDKKey: SDKKey("key"),
					TTL:    NewOptDuration(3 * time.Minute),
				}
			},
			map[string]string{
				"LD_ENV_envname":         "key",
				"LD_TTL_MINUTES_envname": "3",
			},
		)
		testInvalidConfigVars(t,
			map[string]string{
				"LD_ENV_envname":         "key",
				"LD_TTL_MINUTES_envname": "x",
			},
			"LD_TTL_MINUTES_envname: must be an integer",
		)
	})

	t.Run("database CACHE_TTL as number of milliseconds", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) {
				c.Redis.URL = defaultRedisURL
				c.Redis.LocalTTL = NewOptDuration(500 * time.Millisecond)
			},
			map[string]string{
				"USE_REDIS": "1",
				"CACHE_TTL": "500",
			},
		)
		testValidConfigVars(t,
			func(c *Config) {
				c.DynamoDB.Enabled = true
				c.DynamoDB.LocalTTL = NewOptDuration(500 * time.Millisecond)
			},
			map[string]string{
				"USE_DYNAMODB": "1",
				"CACHE_TTL":    "500",
			},
		)
		testValidConfigVars(t,
			func(c *Config) {
				c.Consul.Host = defaultConsulHost
				c.Consul.LocalTTL = NewOptDuration(500 * time.Millisecond)
			},
			map[string]string{
				"USE_CONSUL": "1",
				"CACHE_TTL":  "500",
			},
		)
	})

	t.Run("REDIS_TTL as number of milliseconds", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) {
				c.Redis.URL = defaultRedisURL
				c.Redis.LocalTTL = NewOptDuration(500 * time.Millisecond)
			},
			map[string]string{
				"USE_REDIS": "1",
				"REDIS_TTL": "500",
			},
		)
		testValidConfigVars(t,
			func(c *Config) {
				c.Redis.URL = defaultRedisURL
				c.Redis.LocalTTL = NewOptDuration(300 * time.Millisecond)
			},
			map[string]string{
				"USE_REDIS": "1",
				"REDIS_TTL": "300",
				"CACHE_TTL": "500",
			},
		)
	})
}

func TestConfigFromEnvironmentFieldValidation(t *testing.T) {
	t.Run("allows boolean values 0/1 or true/false", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) { c.Main.ExitOnError = true },
			map[string]string{"EXIT_ON_ERROR": "true"},
		)
		testValidConfigVars(t,
			func(c *Config) { c.Main.ExitOnError = true },
			map[string]string{"EXIT_ON_ERROR": "1"},
		)
		testValidConfigVars(t,
			func(c *Config) { c.Main.ExitOnError = false },
			map[string]string{"EXIT_ON_ERROR": "false"},
		)
		testValidConfigVars(t,
			func(c *Config) { c.Main.ExitOnError = false },
			map[string]string{"EXIT_ON_ERROR": "0"},
		)
	})

	t.Run("treats unrecognized boolean values as false", func(t *testing.T) {
		// TODO: not sure this is desirable
		testValidConfigVars(t,
			func(c *Config) { c.Main.ExitOnError = false },
			map[string]string{"EXIT_ON_ERROR": "not really"},
		)
	})

	t.Run("parses valid int", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) { c.Main.Port = 222 },
			map[string]string{"PORT": "222"},
		)
	})

	t.Run("rejects invalid int", func(t *testing.T) {
		testInvalidConfigVars(t,
			map[string]string{"PORT": "not-numeric"},
			"PORT: must be an integer",
		)
	})

	t.Run("parses valid URI", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) { c.Main.BaseURI = newOptAbsoluteURLMustBeValid("http://some/uri") },
			map[string]string{"BASE_URI": "http://some/uri"},
		)
	})

	t.Run("rejects invalid URI", func(t *testing.T) {
		testInvalidConfigVars(t,
			map[string]string{"BASE_URI": "::"},
			"BASE_URI: not a valid URL/URI",
		)
		testInvalidConfigVars(t,
			map[string]string{"BASE_URI": "not/absolute"},
			"BASE_URI: must be an absolute URL/URI",
		)
	})

	t.Run("parses valid duration", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) { c.Main.HeartbeatInterval = NewOptDuration(3 * time.Second) },
			map[string]string{"HEARTBEAT_INTERVAL": "3s"},
		)
	})

	t.Run("rejects invalid duration", func(t *testing.T) {
		testInvalidConfigVars(t,
			map[string]string{"HEARTBEAT_INTERVAL": "x"},
			"HEARTBEAT_INTERVAL: "+errBadDuration("x").Error(),
		)
	})

	t.Run("parses valid log level", func(t *testing.T) {
		testValidConfigVars(t,
			func(c *Config) { c.Main.LogLevel = NewOptLogLevel(ldlog.Warn) },
			map[string]string{"LOG_LEVEL": "warn"},
		)
		testValidConfigVars(t,
			func(c *Config) { c.Main.LogLevel = NewOptLogLevel(ldlog.Error) },
			map[string]string{"LOG_LEVEL": "eRrOr"},
		)
	})

	t.Run("rejects invalid log level", func(t *testing.T) {
		testInvalidConfigVars(t,
			map[string]string{"LOG_LEVEL": "wrong"},
			`LOG_LEVEL: "wrong" is not a valid log level`,
		)
	})
}

func testValidConfigVars(t *testing.T, buildConfig func(c *Config), vars map[string]string) {
	withEnvironment(vars, func() {
		expectedConfig := DefaultConfig
		buildConfig(&expectedConfig)

		c := DefaultConfig
		err := LoadConfigFromEnvironment(&c, ldlog.NewDisabledLoggers())
		require.NoError(t, err)

		assert.Equal(t, expectedConfig, c)
	})
}

func testInvalidConfigVars(t *testing.T, vars map[string]string, errMessage string) {
	withEnvironment(vars, func() {
		c := DefaultConfig
		err := LoadConfigFromEnvironment(&c, ldlog.NewDisabledLoggers())
		require.Error(t, err)
		assert.Equal(t, errMessage, err.Error())
	})
}

func withEnvironment(vars map[string]string, action func()) {
	saved := make(map[string]string)
	for _, kv := range os.Environ() {
		p := strings.Index(kv, "=")
		saved[kv[:p]] = kv[p+1:]
	}
	defer func() {
		os.Clearenv()
		for k, v := range saved {
			os.Setenv(k, v)
		}
	}()
	os.Clearenv()
	for k, v := range vars {
		os.Setenv(k, v)
	}
	action()
}
