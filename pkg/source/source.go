// Package source provides the Source interface and a scripted, deterministic
// simulated device used in local runs and tests.
package source

import (
	"context"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/clock"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

// Source is a single sensor data source.
type Source interface {
	ID() sensor.SourceID
	Descriptor() Descriptor

	// Run emits samples (or an error) on the returned channels until ctx
	// is canceled or the scripted timeline is exhausted. A device restart
	// is represented by an Epoch bump in the stream; failures by Err().
	Run(ctx context.Context) (<-chan sensor.Sample, <-chan error)

	// Inject pushes one externally-scripted sample into the source. Used
	// for deterministic replay tests.
	Inject(s sensor.Sample) error

	// Fail marks the simulated device as failed until Restart is called.
	Fail()

	// Restart bumps the device epoch; subsequent sequence numbers restart
	// from the script's next value.
	Restart()

	// Err reports the current terminal/temporary failure, if any.
	Err() error
}

// Descriptor is the static configuration of a source.
type Descriptor struct {
	ID      sensor.SourceID
	Kind    sensor.Kind
	Unit    sensor.Unit
	Frame   sensor.Frame
	Address string
	Period  time.Duration // nominal sampling period (used for gap detection)
}

// ScriptedSample is one entry of a simulated device timeline.
type ScriptedSample struct {
	Epoch        uint64
	Seq          uint64
	SampleTS     time.Time // zero => derived from clock + period
	Quality      float64
	M            sensor.Measurement
	GapAfter     bool // drop the next expected sample (simulate loss)
	RestartAfter bool // bump epoch after emitting this sample
	FailAfter    bool // fail the device after emitting this sample
}

// Options configures a simulated source.
type Options struct {
	Desc   Descriptor
	Clock  clock.Clock
	Script []ScriptedSample
}
