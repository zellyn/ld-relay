package ldclient

import (
	"time"
)

// An Event represents an analytics event generated by the client, which will be passed to
// the EventProcessor.  The event data that the EventProcessor actually sends to LaunchDarkly
// may be slightly different.
type Event interface {
	GetBase() BaseEvent
}

// BaseEvent provides properties common to all events.
type BaseEvent struct {
	CreationDate uint64
	User         User
}

// FeatureRequestEvent is generated by evaluating a feature flag or one of a flag's prerequisites.
type FeatureRequestEvent struct {
	BaseEvent
	Key       string
	Variation *int
	Value     interface{}
	Default   interface{}
	Version   *int
	PrereqOf  *string
	Reason    EvaluationReasonContainer
	// Note, we need to use EvaluationReasonContainer here because FeatureRequestEvent can be
	// deserialized by ld-relay.
	TrackEvents          bool
	Debug                bool
	DebugEventsUntilDate *uint64
}

// CustomEvent is generated by calling the client's Track method.
type CustomEvent struct {
	BaseEvent
	Key  string
	Data interface{}
}

// IdentifyEvent is generated by calling the client's Identify method.
type IdentifyEvent struct {
	BaseEvent
}

// IndexEvent is generated internally to capture user details from other events.
type IndexEvent struct {
	BaseEvent
}

// NewFeatureRequestEvent creates a feature request event. Normally, you don't need to call this;
// the event is created and queued automatically during feature flag evaluation.
func NewFeatureRequestEvent(key string, flag *FeatureFlag, user User, variation *int, value, defaultVal interface{}, prereqOf *string) FeatureRequestEvent {
	fre := FeatureRequestEvent{
		BaseEvent: BaseEvent{
			CreationDate: now(),
			User:         user,
		},
		Key:       key,
		Variation: variation,
		Value:     value,
		Default:   defaultVal,
		PrereqOf:  prereqOf,
	}
	if flag != nil {
		fre.Version = &flag.Version
		fre.TrackEvents = flag.TrackEvents
		fre.DebugEventsUntilDate = flag.DebugEventsUntilDate
	}
	return fre
}

// GetBase returns the BaseEvent
func (evt FeatureRequestEvent) GetBase() BaseEvent {
	return evt.BaseEvent
}

// NewCustomEvent constructs a new custom event, but does not send it. Typically, Track should be used to both create the
// event and send it to LaunchDarkly.
func NewCustomEvent(key string, user User, data interface{}) CustomEvent {
	return CustomEvent{
		BaseEvent: BaseEvent{
			CreationDate: now(),
			User:         user,
		},
		Key:  key,
		Data: data,
	}
}

// GetBase returns the BaseEvent
func (evt CustomEvent) GetBase() BaseEvent {
	return evt.BaseEvent
}

// NewIdentifyEvent constructs a new identify event, but does not send it. Typically, Identify should be used to both create the
// event and send it to LaunchDarkly.
func NewIdentifyEvent(user User) IdentifyEvent {
	return IdentifyEvent{
		BaseEvent: BaseEvent{
			CreationDate: now(),
			User:         user,
		},
	}
}

// GetBase returns the BaseEvent
func (evt IdentifyEvent) GetBase() BaseEvent {
	return evt.BaseEvent
}

// GetBase returns the BaseEvent
func (evt IndexEvent) GetBase() BaseEvent {
	return evt.BaseEvent
}

func now() uint64 {
	return toUnixMillis(time.Now())
}

func toUnixMillis(t time.Time) uint64 {
	ms := time.Duration(t.UnixNano()) / time.Millisecond

	return uint64(ms)
}
