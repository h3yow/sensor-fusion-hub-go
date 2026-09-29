// Package calibrate holds per-sensor calibration parameters and the
// versioned parameter registry. Every parameter change creates a new,
// immutable epoch so old samples are never re-computed with new params
// and vice versa.
package calibrate

import (
	"errors"
	"math"
	"sync"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

// Params are the calibration parameters for one source.
//
// The calibrated value is (v_raw + Bias) * Scale, applied component-wise.
//
// Temperature uses Bias[0]/Scale[0] only.
// IMU applies Bias[0..2]/Scale[0..2] to gyro and acc independently.
// GPS uses Bias[0..2] only (scale fixed at 1).
type Params struct {
	Bias  [3]float64
	Scale [3]float64
}

// IdentityParams returns unit-scale, zero-bias parameters.
func IdentityParams() Params {
	return Params{Scale: [3]float64{1, 1, 1}}
}

// Entry is one immutable revision of a source's calibration parameters.
type Entry struct {
	Epoch     uint64
	SourceID  sensor.SourceID
	Params    Params
	AppliedAt uint64 // global logical sequence number of the update
}

// Registry stores the current calibration epoch per source and the full
// history of parameter entries.
type Registry struct {
	mu      sync.RWMutex
	entries map[sensor.SourceID][]Entry
	kinds   map[sensor.SourceID]sensor.Kind
	current map[sensor.SourceID]uint64
	applied uint64
}

// NewRegistry constructs an empty registry; sources get epoch 0 identity
// parameters when registered.
func NewRegistry() *Registry {
	return &Registry{
		entries: map[sensor.SourceID][]Entry{},
		kinds:   map[sensor.SourceID]sensor.Kind{},
		current: map[sensor.SourceID]uint64{},
	}
}

// Register adds a source at epoch 0 with identity parameters.
// Re-registering an existing ID is an error.
func (r *Registry) Register(id sensor.SourceID, k sensor.Kind) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; ok {
		return sensor.ErrDuplicateSource
	}
	r.entries[id] = []Entry{{
		Epoch:     0,
		SourceID:  id,
		Params:    IdentityParams(),
		AppliedAt: r.applied,
	}}
	r.kinds[id] = k
	r.current[id] = 0
	return nil
}

// Update validates and stores a new parameter revision for a source and
// returns the new epoch number. Samples ingested before the update keep
// the previous epoch tag; samples ingested afterwards receive the new tag.
// Old and new data therefore never cross-compute.
func (r *Registry) Update(id sensor.SourceID, p Params) (uint64, error) {
	r.mu.RLock()
	k, ok := r.kinds[id]
	r.mu.RUnlock()
	if !ok {
		return 0, sensor.ErrUnknownSource
	}
	if err := Validate(k, p); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Re-read under lock (Register cannot remove entries, so hist stays
	// valid); the new epoch is strictly monotonic.
	r.applied++
	epoch := r.current[id] + 1
	entry := Entry{Epoch: epoch, SourceID: id, Params: p, AppliedAt: r.applied}
	r.entries[id] = append(r.entries[id], entry)
	r.current[id] = epoch
	return epoch, nil
}

// EpochAt returns the calibration epoch active at global logical sequence
// seq. With monotonically appended entries, later updates never rewrite
// earlier answers.
func (r *Registry) EpochAt(id sensor.SourceID, seq uint64) (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hist, ok := r.entries[id]
	if !ok {
		return 0, false
	}
	epoch := uint64(0)
	for _, e := range hist {
		if e.AppliedAt <= seq {
			epoch = e.Epoch
		}
	}
	return epoch, true
}

// CurrentEpoch returns the latest epoch for a source.
func (r *Registry) CurrentEpoch(id sensor.SourceID) (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.current[id]
	return e, ok
}

// ParamsAt returns the parameters that were active at a given epoch.
func (r *Registry) ParamsAt(id sensor.SourceID, epoch uint64) (Params, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hist, ok := r.entries[id]
	if !ok {
		return Params{}, false
	}
	for _, e := range hist {
		if e.Epoch == epoch {
			return e.Params, true
		}
	}
	return Params{}, false
}

// Apply calibrates one normalized measurement with the given parameters.
// It is a pure function and its result depends only on its inputs, so
// replay is reproducible.
func Apply(p Params, k sensor.Kind, in sensor.Measurement) sensor.Measurement {
	out := in
	s := effectiveScale(p)
	switch k {
	case sensor.KindTemperature:
		out.Temperature = (in.Temperature + p.Bias[0]) * s[0]
	case sensor.KindIMU:
		out.Gyro = sensor.Vec3{
			X: (in.Gyro.X + p.Bias[0]) * s[0],
			Y: (in.Gyro.Y + p.Bias[1]) * s[1],
			Z: (in.Gyro.Z + p.Bias[2]) * s[2],
		}
		out.Acc = sensor.Vec3{
			X: (in.Acc.X + p.Bias[0]) * s[0],
			Y: (in.Acc.Y + p.Bias[1]) * s[1],
			Z: (in.Acc.Z + p.Bias[2]) * s[2],
		}
	case sensor.KindGPS:
		out.Lat = in.Lat + p.Bias[0]
		out.Lon = in.Lon + p.Bias[1]
		out.Alt = in.Alt + p.Bias[2]
		out.X = in.X + p.Bias[0]
		out.Y = in.Y + p.Bias[1]
		out.Z = in.Z + p.Bias[2]
	}
	return out
}

func effectiveScale(p Params) [3]float64 {
	s := p.Scale
	for i := range s {
		if s[i] == 0 {
			s[i] = 1 // unset per-axis scale means identity
		}
	}
	return s
}

// Validate checks parameter sanity for the generic 3-vector case.
func Validate(k sensor.Kind, p Params) error {
	return ValidateKind(k, p)
}

// ValidateKind checks parameter sanity for the given kind: finite numbers,
// non-zero finite scale factors.
func ValidateKind(k sensor.Kind, p Params) error {
	for i := 0; i < 3; i++ {
		if math.IsNaN(p.Bias[i]) || math.IsInf(p.Bias[i], 0) {
			return errors.New("calibrate: non-finite bias")
		}
	}
	// Only the components meaningful for the kind are checked; unused
	// trailing components are required to stay at their zero/identity value
	// but zero does NOT mean "invalid" for an unused component.
	checkAxis := func(i int) error {
		if math.IsNaN(p.Scale[i]) || math.IsInf(p.Scale[i], 0) {
			return errors.New("calibrate: non-finite scale factor")
		}
		return nil
	}
	switch k {
	case sensor.KindTemperature:
		if p.Bias[1] != 0 || p.Bias[2] != 0 || p.Scale[1] != 0 || p.Scale[2] != 0 {
			return errors.New("calibrate: temperature params must use component 0 only")
		}
		if err := checkAxis(0); err != nil {
			return err
		}
	case sensor.KindGPS:
		for i := 0; i < 3; i++ {
			if p.Scale[i] != 0 && p.Scale[i] != 1 {
				return errors.New("calibrate: gps scale must be 1 (or 0 = identity)")
			}
			if err := checkAxis(i); err != nil {
				return err
			}
		}
	default: // IMU: all three axes are used; zero scale is permitted only
		// when the whole scale vector is unset (identity implied).
		scaleSet := p.Scale != [3]float64{}
		if !scaleSet {
			return nil
		}
		for i := 0; i < 3; i++ {
			if err := checkAxis(i); err != nil {
				return err
			}
			if p.Scale[i] == 0 {
				return errors.New("calibrate: invalid zero scale factor")
			}
		}
	}
	return nil
}
