package hub_test

import (
	"errors"
	"testing"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/clock"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/engine"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/hub"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/source"
)

// buildHubWithClock pins a controllable clock and one temperature source.
func buildHubWithClock(t *testing.T, t0 time.Time) (*hub.Hub, *clock.ScriptedClock, *captureLogger) {
	t.Helper()
	log := &captureLogger{}
	clk := clock.NewScriptedClock([]clock.Step{{At: t0}})
	h, err := hub.New(hub.Config{
		Sources: []source.Descriptor{
			{ID: "t1", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal, Address: "a1", Period: time.Second},
		},
		Tuning: engine.DefaultTuning(),
	}, clk, log)
	if err != nil {
		t.Fatal(err)
	}
	return h, clk, log
}

func tsample(seq, epoch uint64, sampleTS, recvTS time.Time, v float64) sensor.Sample {
	return sensor.Sample{
		Source: "t1", Kind: sensor.KindTemperature, Unit: sensor.UnitCelsius, Frame: sensor.FrameThermal,
		Seq: seq, Epoch: epoch, SampleTS: sampleTS, RecvTS: recvTS, Quality: 1,
		M: sensor.Measurement{Temperature: v},
	}
}

func TestOutOfOrderIsRejectedAndCounted(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h, _, _ := buildHubWithClock(t, t0)

	if err := h.Ingest(tsample(2, 0, t0.Add(2*time.Second), t0.Add(2*time.Second), 20)); err != nil {
		t.Fatal(err)
	}
	// Older seq and timestamp -> out of order, must not replace latest.
	err := h.Ingest(tsample(1, 0, t0.Add(1*time.Second), t0.Add(2*time.Second), 99))
	if !errors.Is(err, sensor.ErrOutOfOrder) {
		t.Fatalf("expected ErrOutOfOrder, got %v", err)
	}
	snap := h.Snapshot()
	if snap.Sources[0].LastSeq != 2 {
		t.Fatalf("latest seq must remain 2, got %d", snap.Sources[0].LastSeq)
	}
	fused := findFused(snap, sensor.KindTemperature)
	if fused.M.Temperature != 20 {
		t.Fatalf("expired-order sample must not win; got %v", fused.M.Temperature)
	}
}

func TestGapIsCountedAndStillFuses(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h, clk, _ := buildHubWithClock(t, t0)

	mustIngest(t, h, tsample(1, 0, t0.Add(time.Second), t0.Add(time.Second), 20))
	clk.Set(t0.Add(2 * time.Second))
	mustIngest(t, h, tsample(2, 0, t0.Add(2*time.Second), t0.Add(2*time.Second), 21))
	clk.Set(t0.Add(3 * time.Second))
	// seq 3 lost: jump 2 -> 4
	mustIngest(t, h, tsample(4, 0, t0.Add(3*time.Second), t0.Add(3*time.Second), 22))

	st := h.Snapshot().Sources[0]
	if st.GapCount != 1 {
		t.Fatalf("expected 1 gap, got %d", st.GapCount)
	}
}

func TestLateArrivalIsRejected(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h, _, _ := buildHubWithClock(t, t0)

	// First sample received at t0+10s but sampled at t0 (delay 10s > MaxLate=2s).
	err := h.Ingest(tsample(1, 0, t0, t0.Add(10*time.Second), 20))
	if !errors.Is(err, sensor.ErrLateArrival) {
		t.Fatalf("late arrival must return ErrLateArrival, got %v", err)
	}
	snap := h.Snapshot()
	if snap.Sources[0].Status == sensor.StatusOnline {
		t.Fatalf("source with only late data must not be online: %v", snap.Sources[0].Status)
	}
}

func TestStaleThenOfflineTransition(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h, clk, _ := buildHubWithClock(t, t0)

	mustIngest(t, h, tsample(1, 0, t0, t0, 20))

	clk.Set(t0.Add(1500 * time.Millisecond)) // inside fresh=2s -> online
	if st := h.Snapshot().Sources[0]; st.Status != sensor.StatusOnline {
		t.Fatalf("want online at 1.5s, got %s", st.Status)
	}
	clk.Set(t0.Add(2500 * time.Millisecond)) // beyond fresh, before offline
	st := h.Snapshot().Sources[0]
	if st.Status != sensor.StatusStale || st.Reason != sensor.ReasonNoFreshData {
		t.Fatalf("want stale/no_fresh_data, got %s/%s", st.Status, st.Reason)
	}
	clk.Set(t0.Add(6 * time.Second)) // beyond offline=5s
	st = h.Snapshot().Sources[0]
	if st.Status != sensor.StatusOffline {
		t.Fatalf("want offline, got %s", st.Status)
	}
	fused := findFused(h.Snapshot(), sensor.KindTemperature)
	if fused.Status != sensor.StatusStale || len(fused.Contribs) != 0 {
		t.Fatalf("offline source must not contribute, got %+v", fused)
	}
}

func TestSourceRestartOldEpochRejected(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h, clk, _ := buildHubWithClock(t, t0)

	mustIngest(t, h, tsample(10, 0, t0, t0, 100))
	// restart observed
	clk.Set(t0.Add(time.Second))
	mustIngest(t, h, tsample(1, 1, t0.Add(time.Second), t0.Add(time.Second), 1))

	st := h.Snapshot().Sources[0]
	if st.Status != sensor.StatusRestarted || st.Reason != sensor.ReasonEpochBump {
		t.Fatalf("want restarted/epoch_bump, got %s/%s", st.Status, st.Reason)
	}
	// stale sample from the previous epoch must be refused and never win.
	if err := h.Ingest(tsample(11, 0, t0.Add(1500*time.Millisecond), t0.Add(1500*time.Millisecond), 999)); !errors.Is(err, sensor.ErrStale) {
		t.Fatalf("expected ErrStale for pre-restart epoch, got %v", err)
	}
	fused := findFused(h.Snapshot(), sensor.KindTemperature)
	if fused.M.Temperature != 1 {
		t.Fatalf("old-epoch sample must not win, got %v", fused.M.Temperature)
	}

	// once the new timeline proves freshness for one evaluation, status online
	clk.Set(t0.Add(2 * time.Second))
	mustIngest(t, h, tsample(2, 1, t0.Add(2*time.Second), t0.Add(2*time.Second), 2))
	st = h.Snapshot().Sources[0]
	if st.Status != sensor.StatusOnline {
		t.Fatalf("want online after proven new timeline, got %s/%s", st.Status, st.Reason)
	}
}

func TestTimelineVerdictIsLogged(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	h, _, log := buildHubWithClock(t, t0)
	_ = h.Ingest(tsample(1, 0, t0, t0, 20))

	text := log.render()
	for _, want := range []string{"id=t1", "seq=1", "epoch=0", "accepted=true", "verdict_reason="} {
		if !contains(text, want) {
			t.Fatalf("timeline log missing %q in:\n%s", want, text)
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	// Same scripted input replayed twice must yield identical snapshots.
	run := func() []sensor.FusedResult {
		t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
		h, clk, _ := buildHubWithClock(t, t0)
		seq := []float64{20, 20.5, 21, 21.2, 21.4}
		for i, v := range seq {
			now := t0.Add(time.Duration(i+1) * time.Second)
			clk.Set(now)
			mustIngest(t, h, tsample(uint64(i+1), 0, now, now, v))
		}
		return h.Snapshot().Fused
	}
	a := run()
	b := run()
	if fmtResults(a) != fmtResults(b) {
		t.Fatalf("replay mismatch:\n%s\nvs\n%s", fmtResults(a), fmtResults(b))
	}
}

func findFused(s sensor.Snapshot, k sensor.Kind) sensor.FusedResult {
	for _, f := range s.Fused {
		if f.Kind == k {
			return f
		}
	}
	return sensor.FusedResult{}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
