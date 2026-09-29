package sensor

import "errors"

// Deterministic, inspectable configuration / data errors.
var (
	// ErrDuplicateSource is returned when a source ID is registered twice.
	ErrDuplicateSource = errors.New("sensor: duplicate source id")

	// ErrUnknownSource is returned when data arrives from an unregistered source.
	ErrUnknownSource = errors.New("sensor: unknown source")

	// ErrUnsupportedUnit is returned when a source's unit is not supported
	// for its kind.
	ErrUnsupportedUnit = errors.New("sensor: unsupported unit for kind")

	// ErrUnsupportedFrame is returned when the frame is not supported for
	// the kind.
	ErrUnsupportedFrame = errors.New("sensor: unsupported frame for kind")

	// ErrConfigConflict is returned when two sources of the same kind
	// declare incompatible canonical units/frames or colliding addresses.
	ErrConfigConflict = errors.New("sensor: configuration conflict")

	// ErrBadConfig is returned for invalid configuration values.
	ErrBadConfig = errors.New("sensor: invalid configuration")

	// ErrOutOfOrder is returned (to the caller of Ingest) when a sample's
	// device timestamp is older than the latest accepted one in the epoch.
	ErrOutOfOrder = errors.New("sensor: out-of-order sample")

	// ErrStale is returned when a sample belongs to an expired timeline
	// (old epoch after a restart was observed).
	ErrStale = errors.New("sensor: sample from superseded epoch")

	// ErrLateArrival is returned when a sample arrives more than MaxLate
	// after its own sample time and can never be the latest observation.
	ErrLateArrival = errors.New("sensor: late arrival beyond max late")

	// ErrNoData is returned when a snapshot is requested for a kind that
	// currently has no usable data.
	ErrNoData = errors.New("sensor: no usable data")
)
