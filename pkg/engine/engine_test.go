package engine_test

import (
	"testing"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/calibrate"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/engine"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

func ns(id sensor.SourceID, seq, epoch uint64, recv time.Time, q float64, v float64) sensor.NormalizedSample {
	return sensor.NormalizedSample{
		Sample: sensor.Sample{
			Source: id, Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal,
			Seq: seq, Epoch: 1, SampleTS: recv, RecvTS: recv, Quality: q,
			M: sensor.Measurement{Temperature: v},
		},
		CalEpoch: epoch,
	}
}

func TestWeightedFusionAndDegradation(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	reg := calibrate.NewRegistry()
	_ = reg.Register("a", sensor.KindTemperature)
	_ = reg.Register("b", sensor.KindTemperature)

	e := engine.New(reg, engine.DefaultTuning())
	if err := e.AddSource("a", engine.SourceDescriptor{Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal}); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSource("b", engine.SourceDescriptor{Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal}); err != nil {
		t.Fatal(err)
	}

	now := t0
	if _, err := e.Ingest(ns("a", 1, 0, now, 1, 20)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ingest(ns("b", 1, 0, now, 1, 22)); err != nil {
		t.Fatal(err)
	}
	snap := e.Snapshot(now, nil)
	f, ok := fused(snap, sensor.KindTemperature)
	if !ok {
		t.Fatal("no temperature fusion")
	}
	if f.Status != sensor.StatusOnline || len(f.Contribs) != 2 || f.M.Temperature != 21 {
		t.Fatalf("2-source fusion: %+v", f)
	}
	// deterministic contributor ordering
	if f.Contribs[0] != "a" || f.Contribs[1] != "b" {
		t.Fatalf("contributor order: %v", f.Contribs)
	}

	// b stale -> single-source degraded
	later := t0.Add(3 * time.Second)
	if _, err := e.Ingest(ns("a", 2, 0, later, 1, 24)); err != nil {
		t.Fatal(err)
	}
	snap = e.Snapshot(later, nil)
	f, ok = fused(snap, sensor.KindTemperature)
	if !ok {
		t.Fatal("no temperature fusion later")
	}
	if f.Status != sensor.StatusDegraded || len(f.Contribs) != 1 || f.M.Temperature != 24 {
		t.Fatalf("single survivor fusion: %+v", f)
	}

	// both stale -> stale result, no contributions
	snap = e.Snapshot(t0.Add(20*time.Second), nil)
	f, ok = fused(snap, sensor.KindTemperature)
	if !ok {
		t.Fatal("no temperature fusion at 20s")
	}
	if f.Status != sensor.StatusStale || len(f.Contribs) != 0 {
		t.Fatalf("all-stale fusion: %+v", f)
	}
}

func TestQualityFloorExcludesSource(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	reg := calibrate.NewRegistry()
	_ = reg.Register("a", sensor.KindTemperature)
	e := engine.New(reg, engine.DefaultTuning())
	_ = e.AddSource("a", engine.SourceDescriptor{Kind: sensor.KindTemperature})
	if _, err := e.Ingest(ns("a", 1, 0, t0, 0.1, 20)); err != nil {
		t.Fatal(err)
	}
	snap := e.Snapshot(t0, nil)
	st := snap.Sources[0]
	if st.Status != sensor.StatusDegraded || st.Reason != sensor.ReasonQualityLow {
		t.Fatalf("state: %s/%s", st.Status, st.Reason)
	}
	f, found := fused(snap, sensor.KindTemperature)
	if !found {
		t.Fatal("no temperature fusion for quality case")
	}
	if len(f.Contribs) != 0 {
		t.Fatalf("low quality must not contribute: %+v", f)
	}
}

func TestUnknownSourceIngest(t *testing.T) {
	e := engine.New(calibrate.NewRegistry(), engine.DefaultTuning())
	if _, err := e.Ingest(ns("x", 1, 0, time.Now(), 1, 1)); err == nil {
		t.Fatal("unknown source must error")
	}
}

func fused(s sensor.Snapshot, k sensor.Kind) (sensor.FusedResult, bool) {
	for _, f := range s.Fused {
		if f.Kind == k {
			return f, true
		}
	}
	return sensor.FusedResult{}, false
}
