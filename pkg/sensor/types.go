// Package sensor defines the core data model: sensor sources, samples,
// physical units/coordinate systems and deterministic error types.
package sensor

import (
	"time"
)

// SourceID uniquely identifies a physical or simulated sensor device.
type SourceID string

// Kind categorizes a sensor by the physical quantity it measures.
type Kind string

// Supported sensor kinds.
const (
	KindTemperature Kind = "temperature"
	KindIMU         Kind = "imu"
	KindGPS         Kind = "gps"
)

// Unit identifies the measurement unit of a sample.
type Unit string

// Supported units per kind.
const (
	UnitCelsius    Unit = "C"
	UnitFahrenheit Unit = "F"
	UnitKelvin     Unit = "K"

	UnitRPS  Unit = "rad/s" // IMU gyroscope
	UnitDPS  Unit = "deg/s" // IMU gyroscope
	UnitMPS2 Unit = "m/s^2" // IMU accelerometer
	UnitG    Unit = "g"     // IMU accelerometer

	UnitLLA  Unit = "lat,lon,alt" // GPS: degrees, degrees, meters
	UnitECEF Unit = "ecef"        // GPS: earth-centered earth-fixed meters
)

// Frame identifies the coordinate / reference frame of a sample.
type Frame string

// Supported reference frames.
const (
	FrameBody    Frame = "body"    // sensor/body frame
	FrameENU     Frame = "enu"     // local east-north-up
	FrameNED     Frame = "ned"     // local north-east-down
	FrameWGS84   Frame = "wgs84"   // GPS geographic frame
	FrameECEF    Frame = "ecef"    // earth-centered earth-fixed
	FrameThermal Frame = "thermal" // scalar temperatures need no transform
)

// Status describes the observable health/quality state of a source.
type Status string

// Distinguishable source statuses.
const (
	StatusUnknown   Status = "unknown"
	StatusOnline    Status = "online"
	StatusStale     Status = "stale"     // no fresh data within freshness window
	StatusDegraded  Status = "degraded"  // low quality reported by the source
	StatusRestarted Status = "restarted" // source epoch advanced (device restart)
	StatusOffline   Status = "offline"   // declared dead after timeout
	StatusRejected  Status = "rejected"  // registration or configuration rejected
)

// Reason is a machine-readable explanation attached to a status transition.
type Reason string

const (
	ReasonNone              Reason = ""
	ReasonRegistration      Reason = "registration_error"
	ReasonOutOfOrder        Reason = "out_of_order"
	ReasonGap               Reason = "data_gap"
	ReasonLate              Reason = "late_arrival"
	ReasonExpired           Reason = "expired"
	ReasonEpochBump         Reason = "epoch_bump"
	ReasonNoFreshData       Reason = "no_fresh_data"
	ReasonQualityLow        Reason = "quality_low"
	ReasonCalibrationChange Reason = "calibration_change"
)

// Vec3 is a 3-component vector (used by IMU/GPS measurements).
type Vec3 struct {
	X, Y, Z float64
}

// Measurement holds the kind-specific payload of a sample.
// Only the field matching the sample Kind is populated.
type Measurement struct {
	// Temperature (Celsius after normalization).
	Temperature float64
	// IMU: gyroscope (rad/s) and accelerometer (m/s^2) in the declared frame.
	Gyro Vec3
	Acc  Vec3
	// GPS: latitude/longitude in degrees, altitude in meters (WGS84 LLA),
	// or ECEF X/Y/Z in meters.
	Lat, Lon, Alt float64
	X, Y, Z       float64
}

// Sample is a single timestamped observation from one source.
//
// SampleTS is the device-side acquisition time; RecvTS is when the hub
// received it. Samples are ordered/validated against SampleTS, while
// freshness is evaluated against RecvTS using a controllable clock.
type Sample struct {
	Source   SourceID
	Kind     Kind
	Unit     Unit
	Frame    Frame
	Seq      uint64    // device monotonic sequence number
	Epoch    uint64    // device boot epoch; bumps across restarts
	SampleTS time.Time // device timestamp
	RecvTS   time.Time // local receive timestamp (filled by the source/hub)
	Quality  float64   // 0..1, device-reported confidence
	M        Measurement
}

// NormalizedSample is a sample expressed in the canonical unit/frame
// of its kind, tagged with the calibration epoch that was active when
// it was ingested.
type NormalizedSample struct {
	Sample
	CalEpoch uint64
}

// Snapshot is a consistent point-in-time view of all registered sources
// and the fused outputs computed from the freshest eligible data.
type Snapshot struct {
	TakenAt time.Time
	Sources []SourceState
	Fused   []FusedResult
}

// SourceState is the per-source view inside a snapshot.
type SourceState struct {
	ID        SourceID
	Kind      Kind
	Unit      Unit
	Frame     Frame
	Status    Status
	Reason    Reason
	LastSeq   uint64
	LastTS    time.Time
	LastRecv  time.Time
	Epoch     uint64
	GapCount  int
	LateCount int
}

// FusedResult is the fused output for one sensor kind.
type FusedResult struct {
	Kind     Kind
	Frame    Frame
	M        Measurement
	Status   Status
	Reason   Reason
	Quality  float64
	Contribs []SourceID
	CalEpoch uint64
}

// Logger is the structured-logging interface used across the hub.
//
// InputTimeline is separated from Info so tests can assert that every
// ingest decision logs the input timeline (id/seq/epoch/timestamps) and
// the deterministic verdict (accepted + reason).
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	InputTimeline(msg string, args ...any)
}
