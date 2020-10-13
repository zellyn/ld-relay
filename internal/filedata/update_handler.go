package filedata

import (
	"github.com/launchdarkly/ld-relay/v6/config"
	"github.com/launchdarkly/ld-relay/v6/internal/autoconfig"

	"gopkg.in/launchdarkly/go-server-sdk.v5/interfaces/ldstoretypes"
)

// EnvironmentParams contains all the properties necessary for Relay to create or update an environment,
// except for the flag/segment data. These are identical to the properties defined for auto-configuration,
// so we use the same underlying type.
type EnvironmentParams autoconfig.EnvironmentParams

// UpdateHandler defines the methods that ArchiveManager will call after processing new or updated file data.
type UpdateHandler interface {
	// AddEnvironment is called when the file data has provided a configuration for an environment
	// that ArchiveManager has not seen before.
	AddEnvironment(env ArchiveEnvironment)

	// UpdateEnvironment is called when a change in the file data has provided a new configuration
	// for an existing environment.
	UpdateEnvironment(env ArchiveEnvironment)

	// EnvironmentFailed is called when the ArchiveManager was unable to load the data for an
	// environment.
	EnvironmentFailed(id config.EnvironmentID, err error)

	// DeleteEnvironment is called when a change in the file data has removed an environment.
	DeleteEnvironment(id config.EnvironmentID)
}

// ArchiveEnvironment describes both the environment properties and the SDK data for the environment.
type ArchiveEnvironment struct {
	// Params contains all the properties necessary to create or update the environment, except for
	// the SDK data. These are identical to the properties defined for auto-configuration, so we use
	// the same type.
	Params EnvironmentParams

	// SDKData contains the flags/segments/etc. for populating this environment, in the same format
	// used by the SDK.
	//
	// When updating an environment, if this field is nil, it means that the SDK data for the
	// environment has not changed and only the other environment properties should be updated.
	SDKData []ldstoretypes.Collection
}
