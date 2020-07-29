package relay

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	c "github.com/launchdarkly/ld-relay/v6/config"
	st "github.com/launchdarkly/ld-relay/v6/core/sharedtest"
	"github.com/launchdarkly/ld-relay/v6/internal/version"
	"gopkg.in/launchdarkly/go-sdk-common.v2/ldvalue"
	ld "gopkg.in/launchdarkly/go-server-sdk.v5"
)

func DoStatusEndpointTest(t *testing.T, constructor TestConstructor) {
	var config c.Config
	config.Environment = makeEnvConfigs(testEnvMain, testEnvClientSide, testEnvMobile)

	DoTest(config, constructor, func(p TestParams) {
		r, _ := http.NewRequest("GET", "http://localhost/status", nil)
		result, body := st.DoRequest(r, p.Handler)
		assert.Equal(t, http.StatusOK, result.StatusCode)
		status := ldvalue.Parse(body)

		assertJSONPathMatch(t, "sdk-********-****-****-****-*******e42d0",
			status, "environments", testEnvMain.name, "sdkKey")
		assertJSONPathMatch(t, "connected", status, "environments", testEnvMain.name, "status")

		assertJSONPathMatch(t, "sdk-********-****-****-****-*******e42d1",
			status, "environments", testEnvClientSide.name, "sdkKey")
		assertJSONPathMatch(t, "507f1f77bcf86cd799439011",
			status, "environments", testEnvClientSide.name, "envId")
		assertJSONPathMatch(t, "connected",
			status, "environments", testEnvClientSide.name, "status")

		assertJSONPathMatch(t, "sdk-********-****-****-****-*******e42d2",
			status, "environments", testEnvMobile.name, "sdkKey")
		assertJSONPathMatch(t, "mob-********-****-****-****-*******e42db",
			status, "environments", testEnvMobile.name, "mobileKey")
		assertJSONPathMatch(t, "connected",
			status, "environments", testEnvMobile.name, "status")

		assertJSONPathMatch(t, "healthy", status, "status")
		assertJSONPathMatch(t, version.Version, status, "version")
		assertJSONPathMatch(t, ld.Version, status, "clientVersion")
	})
}

func assertJSONPathMatch(t *testing.T, expected interface{}, inValue ldvalue.Value, path ...string) {
	expectedValue := ldvalue.CopyArbitraryValue(expected)
	value := inValue
	for _, p := range path {
		value = value.GetByKey(p)
	}
	if !expectedValue.Equal(value) {
		assert.Fail(
			t,
			"did not find expected JSON value",
			"at path [%s] in %s\nexpected: %s\nfound: %s",
			strings.Join(path, "."),
			inValue,
			expectedValue,
			value,
		)
	}
}
