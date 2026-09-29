package source_test

import (
	"context"
	"testing"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/clock"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/source"
)

func t0() time.Time { return time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC) }

func TestSimSourceRejectsBadDescriptor(t *testing.T) {
	clk := clock.NewScriptedClock([]clock.Step{{At: t0()}})
	if _, err := source.NewSimSource(source.Options{
		Desc:  source.Descriptor{Kind: sensor.KindTemperature, Unit: "Rankine", Frame: sensor.FrameThermal},
		Clock: clk,
	}); err == nil {
		t.Fatal("unsupported unit must be rejected")
	}
	if _, err := source.NewSimSource(source.Options{
		Desc: source.Descriptor{ID: "x", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal},
	}); err == nil {
		t.Fatal("nil clock must be rejected")
	}
}

func TestSimSourceScriptWithGapFailureRestart(t *testing.T) {
	clk := clock.NewScriptedClock([]clock.Step{{At: t0()}})
	s, err := source.NewSimSource(source.Options{
		Desc:  source.Descriptor{ID: "s1", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal},
		Clock: clk,
		Script: []source.ScriptedSample{
			{Seq: 1, M: sensor.Measurement{Temperature: 1}},
			{Seq: 2, GapAfter: true, M: sensor.Measurement{Temperature: 2}},
			{Seq: 3, M: sensor.Measurement{Temperature: 3}}, // this one is dropped
			{Seq: 4, FailAfter: true, M: sensor.Measurement{Temperature: 4}},
			{Seq: 5, M: sensor.Measurement{Temperature: 5}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	samples, errs := s.Run(ctx)

	got := []uint64{}
	failed := false
loop:
	for {
		select {
		case sa, ok := <-samples:
			if !ok {
				break loop
			}
			got = append(got, sa.Seq)
			if sa.Seq == 4 {
				// wait for failure, then restart with bumped epoch
				select {
				case e := <-errs:
					if e == nil {
						t.Fatal("expected failure error")
					}
					failed = true
				case <-time.After(time.Second):
					t.Fatal("no failure emitted")
				}
				if s.Err() == nil {
					t.Fatal("Err() should report failure")
				}
				s.Restart()
			}
		case <-time.After(time.Second):
			// Script exhausted: cancel so the source closes its channels.
			if len(got) == 4 {
				break loop
			}
			t.Fatalf("timed out, got %v", got)
		}
	}
	if !failed {
		t.Fatal("failure path not exercised")
	}
	want := []uint64{1, 2, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("expected %v (seq3 dropped by gap), got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
	if s.Epoch() == 0 {
		t.Fatal("epoch must bump across restarts")
	}
}

func TestInjectRefusedWhileFailed(t *testing.T) {
	clk := clock.NewScriptedClock([]clock.Step{{At: t0()}})
	s, _ := source.NewSimSource(source.Options{
		Desc:  source.Descriptor{ID: "s1", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal},
		Clock: clk,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = s.Run(ctx)

	s.Fail()
	time.Sleep(10 * time.Millisecond)
	if err := s.Inject(sensor.Sample{Seq: 1}); err != source.ErrDeviceFailed {
		t.Fatalf("inject during failure must error, got %v", err)
	}
	s.Restart()
	if err := s.Inject(sensor.Sample{Seq: 1}); err != nil {
		t.Fatalf("inject after restart should succeed: %v", err)
	}
}
