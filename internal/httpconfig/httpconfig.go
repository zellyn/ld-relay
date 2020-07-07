package httpconfig

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/launchdarkly/ld-relay/v6/config"
	"github.com/launchdarkly/ld-relay/v6/internal/version"
	"github.com/launchdarkly/ld-relay/v6/logging"
	"gopkg.in/launchdarkly/go-server-sdk.v5/interfaces"
	"gopkg.in/launchdarkly/go-server-sdk.v5/ldcomponents"
	"gopkg.in/launchdarkly/go-server-sdk.v5/ldhttp"
	"gopkg.in/launchdarkly/go-server-sdk.v5/ldntlm"
)

// HTTPConfig encapsulates ProxyConfig plus any other HTTP options we may support in the future (currently none).
type HTTPConfig struct {
	config.ProxyConfig
	SDKHTTPConfigFactory interfaces.HTTPConfigurationFactory
	SDKHTTPConfig        interfaces.HTTPConfiguration
}

// NewHTTPConfig validates all of the HTTP-related options and returns an HTTPConfig if successful.
func NewHTTPConfig(proxyConfig config.ProxyConfig, sdkKey string) (HTTPConfig, error) {
	configBuilder := ldcomponents.HTTPConfiguration()
	configBuilder.UserAgent("LDRelay/" + version.Version)

	ret := HTTPConfig{ProxyConfig: proxyConfig}

	if proxyConfig.Url == "" && proxyConfig.NtlmAuth {
		return ret, errors.New("Cannot specify proxy authentication without a proxy URL")
	}
	if proxyConfig.Url != "" {
		_, err := url.Parse(proxyConfig.Url)
		if err != nil {
			return ret, fmt.Errorf("Invalid proxy URL: %s", proxyConfig.Url)
		}
		logging.GlobalLoggers.Infof("Using proxy server at %s", proxyConfig.Url)
	}

	caCertFiles := strings.Split(strings.TrimSpace(proxyConfig.CaCertFiles), ",")

	if proxyConfig.NtlmAuth {
		if proxyConfig.User == "" || proxyConfig.Password == "" {
			return ret, errors.New("NTLM proxy authentication requires username and password")
		}
		transportOpts := []ldhttp.TransportOption{
			ldhttp.ConnectTimeoutOption(ldcomponents.DefaultConnectTimeout),
		}
		for _, filePath := range caCertFiles {
			if filePath != "" {
				transportOpts = append(transportOpts, ldhttp.CACertFileOption(filePath))
			}
		}
		factory, err := ldntlm.NewNTLMProxyHTTPClientFactory(proxyConfig.Url,
			proxyConfig.User, proxyConfig.Password, proxyConfig.Domain, transportOpts...)
		if err != nil {
			return ret, err
		}
		configBuilder.HTTPClientFactory(factory)
		logging.GlobalLoggers.Info("NTLM proxy authentication enabled")
	} else {
		if proxyConfig.Url != "" {
			configBuilder.ProxyURL(proxyConfig.Url)
		}
		for _, filePath := range caCertFiles {
			if filePath != "" {
				configBuilder.CACertFile(filePath)
			}
		}
	}

	var err error
	ret.SDKHTTPConfigFactory = configBuilder
	ret.SDKHTTPConfig, err = configBuilder.CreateHTTPConfiguration(interfaces.BasicConfiguration{SDKKey: sdkKey})
	return ret, err
}

// Client creates a new HTTP client instance that isn't for SDK use.
func (c HTTPConfig) Client() *http.Client {
	return c.SDKHTTPConfig.CreateHTTPClient()
}
