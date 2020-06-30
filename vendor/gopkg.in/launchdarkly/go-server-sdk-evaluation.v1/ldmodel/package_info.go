// Package ldmodel contains the LaunchDarkly Go SDK feature flag data model.
//
// These types correspond to the JSON schema that is used in communication between the SDK and LaunchDarkly
// services, which is also the JSON encoding used for storing flags/segments in a persistent data store.
//
// Normal use of the Go SDK does not require referencing this package directly. It is used internally
// by the SDK, but is published and versioned separately so it can be used in other LaunchDarkly
// components without making the SDK versioning dependent on these internal APIs.
//
// The bulk of the flag evaluation logic is in the main go-server-sdk-evaluation package, rather than in
// these data model types. However, in order to allow certain optimizations that could not be done from
// outside the package without exposing implementation details in the API, some of the logic (such as
// target and clause matching) is implemented here.
package ldmodel
