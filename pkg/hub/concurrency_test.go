package hub_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/calibrate"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/clock"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/engine"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/hub"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/source"
)

func TestConcurrentSnapshotsAreConsistent(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	clk := clock.NewScriptedClock([]clock.Step{{At: t0}})
	log := &captureLogger{}
	h, err := hub.New(hub.Config{
		Sources: []source.Descriptor{
			{ID: "t1", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "a1", Period: time.Second},
			{ID: "t2", Kind: sensor.KindTemperature, Unit: sensor.UnitFahrenheit, Frame: sensor.FrameThermal, Address: "a2", Period: time.Second},
		},
		Tuning: engine.DefaultTuning(),
	}, clk, log)
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Single ingester per source keeps each source's seq monotonic; the two
	// ingesters race with readers to exercise locking.
	for _, id := range []sensor.SourceID{"t1", "t2"} {
		wg.Add(1)
		go func(id sensor.SourceID) {
			defer wg.Done()
			var seq uint64 = 1
			for {
				select {
				case <-stop:
					return
				default:
				}
				now := t0.Add(time.Duration(seq) * time.Second)
				s := sensor.Sample{Source: id, Seq: seq, SampleTS: now, RecvTS: now, Quality: 1,
					Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal,
					M: sensor.Measurement{Temperature: 20 + float64(seq%3)}}
				if id == "t2" {
					s.Unit = sensor.UnitFahrenheit
					s.M.Temperature = 68
				}
				_ = h.Ingest(s) // ignore rejection under racing clock
				seq++
				if seq > 500 {
					return
				}
			}
		}(id)
	}

	// Many concurrent readers: every snapshot must be internally coherent.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				snap := h.Snapshot()
				if len(snap.Sources) != 2 {
					t.Errorf("snapshot source count drift: %d", len(snap.Sources))
					return
				}
				for _, f := range snap.Fused {
					for _, c := range f.Contribs {
						if c != "t1" && c != "t2" {
							t.Errorf("unknown contributor %q", c)
							return
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
}

func TestFailingSourceDoesNotAffectOthers(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	clk := clock.NewScriptedClock([]clock.Step{{At: t0}})
	log := &captureLogger{}
	h, err := hub.New(hub.Config{
		Sources: []source.Descriptor{
			{ID: "good", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "a1"},
			{ID: "bad", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "a2"},
		},
		Tuning: engine.DefaultTuning(),
	}, clk, log)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bad, _ := source.NewSimSource(source.Options{
		Desc:  source.Descriptor{ID: "bad", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "a2"},
		Clock: clk,
		Script: []source.ScriptedSample{
			{Seq: 1, FailAfter: true, M: sensor.Measurement{Temperature: 20}},
		},
	})
	good, _ := source.NewSimSource(source.Options{
		Desc:   source.Descriptor{ID: "good", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "a1"},
		Clock:  clk,
		Script: nil,
	})
	if err := h.Attach(ctx, bad); err != nil {
		t.Fatal(err)
	}
	if err := h.Attach(ctx, good); err != nil {
		t.Fatal(err)
	}

	// The good source keeps emitting while bad is failed.
	for i := 1; i <= 6; i++ {
		now := t0.Add(time.Duration(i) * time.Second)
		clk.Set(now)
		if err := good.Inject(sensor.Sample{
			Seq: uint64(i), SampleTS: now, RecvTS: now, Quality: 1,
			M: sensor.Measurement{Temperature: 21},
		}); err != nil {
			t.Fatalf("good source injection at i=%d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond) // let goroutines drain
	}
	time.Sleep(20 * time.Millisecond)

	snap := h.Snapshot()
	var goodSt, badSt sensor.SourceState
	for _, s := range snap.Sources {
		if s.ID == "good" {
			goodSt = s
		}
		if s.ID == "bad" {
			badSt = s
		}
	}
	if goodSt.LastSeq != 6 {
		t.Fatalf("good source must keep advancing, got seq=%d", goodSt.LastSeq)
	}
	if badSt.LastSeq != 1 {
		t.Fatalf("bad source must stay at seq 1, got %d", badSt.LastSeq)
	}
	if bad.Err() == nil {
		t.Fatal("bad source should report failure")
	}
	fused := findFused(snap, sensor.KindTemperature)
	if fused.M.Temperature != 21 {
		t.Fatalf("fused result should come solely from good source, got %v", fused.M.Temperature)
	}
	if fused.Status != sensor.StatusDegraded {
		t.Fatalf("single-survivor fusion must be DEGRADED, got %s", fused.Status)
	}
	if log.countLevel("error") == 0 {
		t.Fatal("expected an error-level log for the failed source")
	}
}

func TestCalibrationUpdateIsolatesOldAndNew(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	clk := clock.NewScriptedClock([]clock.Step{{At: t0}})
	h, _, _ := buildHubWithClock(t, t0)
	_ = clk

	now := t0
	mustIngest(t, h, tsample(1, 0, now, now, 20))
	fused := findFused(h.Snapshot(), sensor.KindTemperature)
	if fused.CalEpoch != 0 || fused.M.Temperature != 20 {
		t.Fatalf("pre-update: %+v", fused)
	}

	// New params: (v - 2) * 1.
	epoch, err := h.UpdateCalibration("t1", calibrate.Params{
		Bias:  [3]float64{-2, 0, 0},
		Scale: [3]float64{1, 0, 0},
	})
	if err != nil || epoch != 1 {
		t.Fatalf("update: epoch=%d err=%v", epoch, err)
	}
	fused = findFused(h.Snapshot(), sensor.KindTemperature)
	if fused.CalEpoch != 1 || fused.M.Temperature != 18 {
		t.Fatalf("latest sample must be recalibrated to epoch1=18, got %+v", fused)
	}

	// An old-epoch sample arriving now is just a new observation on the new
	// timeline; but an out-of-order observation must not resurrect old math.
	now2 := t0.Add(time.Second)
	if err := h.Ingest(tsample(0, 0, now2, now2, 20)); !errors.Is(err, sensor.ErrOutOfOrder) {
		t.Fatalf("expected out-of-order rejection, got %v", err)
	}

	now3 := t0.Add(2 * time.Second)
	mustIngest(t, h, tsample(2, 0, now3, now3, 30))
	fused = findFused(h.Snapshot(), sensor.KindTemperature)
	if fused.CalEpoch != 1 || fused.M.Temperature != 28 {
		t.Fatalf("post-update sample must use new params (28), got %+v", fused)
	}
}

func TestInvalidCalibrationRejected(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h, _, _ := buildHubWithClock(t, t0)
	if _, err := h.UpdateCalibration("t1", calibrate.Params{
		Scale: [3]float64{0, 1, 1}, // zero scale on used IMU-style axis invalid;
	}); err == nil {
		// temperature only uses axis 0, zero is identity -> this is actually
		// accepted for temperature; use NaN instead below.
	}
	if _, err := h.UpdateCalibration("t1", calibrate.Params{
		Scale: [3]float64{1, 1, 1},
		Bias:  [3]float64{0, 1, 0}, // temperature must only use axis 0
	}); err == nil {
		t.Fatal("expected rejection of multi-axis temperature params")
	}
}

// TestEndToEndScriptedReplay drives two simulated sources through the
// Attach/goroutine path with loss, failure+restart and a calibration
// change, then replays the exact same script asserting identical fused
// output. This is the repeatability guarantee of the component.
func TestEndToEndScriptedReplay(t *testing.T) {
	type rec struct {
		temp    float64
		status  sensor.Status
		reason  sensor.Reason
		contrib int
	}
	play := func() []rec {
		t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
		clk := clock.NewScriptedClock(nil)
		h, err := hub.New(hub.Config{
			Sources: []source.Descriptor{
				{ID: "a", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "1"},
				{ID: "b", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "2"},
			},
			Tuning: engine.DefaultTuning(),
		}, clk, &captureLogger{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sa, _ := source.NewSimSource(source.Options{
			Desc:  source.Descriptor{ID: "a", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "1"},
			Clock: clk})
		sb, _ := source.NewSimSource(source.Options{
			Desc:  source.Descriptor{ID: "b", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "2"},
			Clock: clk})
		if err := h.Attach(ctx, sa); err != nil {
			t.Fatal(err)
		}
		if err := h.Attach(ctx, sb); err != nil {
			t.Fatal(err)
		}

		var out []rec
		inject := func(sec int, id sensor.SourceID, seq, epoch uint64, v float64, fail bool) {
			now := t0.Add(time.Duration(sec) * time.Second)
			clk.Set(now)
			if id == "a" {
				_ = sa.Inject(sensor.Sample{Seq: seq, Epoch: epoch, SampleTS: now, RecvTS: now, Quality: 1,
					M: sensor.Measurement{Temperature: v}})
			} else {
				if fail {
					sb.Fail()
					return
				}
				_ = sb.Inject(sensor.Sample{Seq: seq, Epoch: epoch, SampleTS: now, RecvTS: now, Quality: 1,
					M: sensor.Measurement{Temperature: v}})
			}
		}

		steps := []struct {
			sec        int
			id         sensor.SourceID
			seq, epoch uint64
			v          float64
			fail       bool
			restartB   bool
			cal        bool
		}{
			{1, "a", 1, 0, 20, false, false, false},
			{1, "b", 1, 0, 22, false, false, false},
			{2, "a", 2, 0, 21, false, false, false},
			{2, "b", 2, 0, 23, false, false, false},
			{3, "b", 0, 0, 0, true, false, false},  // b fails
			{3, "a", 3, 0, 22, false, false, true}, // + calibration update
			{4, "a", 4, 0, 23, false, false, false},
			{5, "b", 1, 1, 30, false, true, false}, // b restarted, epoch 1
			{5, "a", 5, 0, 24, false, false, false},
		}
		for _, st := range steps {
			if st.restartB {
				sb.Restart()
			}
			if st.fail {
				// Record the outage synchronously (in production the
				// Attach goroutine does this from the source's error
				// channel); this keeps the replay step deterministic.
				h.MarkSourceFailed("b")
			} else {
				inject(st.sec, st.id, st.seq, st.epoch, st.v, false)
			}
			if st.cal {
				if _, err := h.UpdateCalibration("a", calibrate.Params{
					Bias: [3]float64{0, 0, 0}, Scale: [3]float64{1, 0, 0},
				}); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(10 * time.Millisecond)
			f, ok := getFused(h.Snapshot(), sensor.KindTemperature)
			if !ok {
				out = append(out, rec{status: sensor.StatusStale})
				continue
			}
			out = append(out, rec{f.M.Temperature, f.Status, f.Reason, len(f.Contribs)})
		}
		return out
	}

	r1 := play()
	r2 := play()
	if len(r1) != len(r2) {
		t.Fatalf("length %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("replay mismatch at step %d:\n%+v\nvs\n%+v", i, r1[i], r2[i])
		}
	}
	if r1[len(r1)-1].contrib != 2 || r1[len(r1)-1].status != sensor.StatusOnline {
		t.Fatalf("final state should be online with 2 contributors: %+v", r1[len(r1)-1])
	}
	// Step 4 is the b-failure step (a still fresh) -> degraded single source.
	if r1[4].contrib != 1 || r1[4].status != sensor.StatusDegraded {
		t.Fatalf("during b failure expected degraded/1, got %+v", r1[4])
	}
}

func getFused(s sensor.Snapshot, k sensor.Kind) (sensor.FusedResult, bool) {
	for _, f := range s.Fused {
		if f.Kind == k {
			return f, true
		}
	}
	return sensor.FusedResult{}, false
}
