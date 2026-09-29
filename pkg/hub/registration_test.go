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

func desc(id, kind, unit, frame, addr string) source.Descriptor {
	k := sensor.Kind(kind)
	u := sensor.Unit(unit)
	f := sensor.Frame(frame)
	return source.Descriptor{ID: sensor.SourceID(id), Kind: k, Unit: u, Frame: f, Address: addr, Period: time.Second}
}

func newTestHub(t *testing.T, descs []source.Descriptor) (*hub.Hub, *captureLogger) {
	t.Helper()
	log := &captureLogger{}
	clk := clock.NewScriptedClock([]clock.Step{{At: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}})
	h, err := hub.New(hub.Config{Sources: descs, Tuning: engine.DefaultTuning()}, clk, log)
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	return h, log
}

func TestAcceptsDiverseUnitsOfSameKind(t *testing.T) {
	// Celsius and Fahrenheit temperatures normalize to the same canonical
	// pair and must coexist.
	descs := []source.Descriptor{
		desc("t-c", "temperature", "C", "thermal", "a1"),
		desc("t-f", "temperature", "F", "thermal", "a2"),
		desc("imu-body", "imu", "rad/s", "body", "a3"),
	}
	h, _ := newTestHub(t, descs)

	now := time.Date(2026, 9, 29, 0, 0, 1, 0, time.UTC)
	mustIngest(t, h, sensor.Sample{Source: "t-c", Seq: 1, SampleTS: now, RecvTS: now,
		Quality: 1, M: sensor.Measurement{Temperature: 0}})
	mustIngest(t, h, sensor.Sample{Source: "t-f", Seq: 1, SampleTS: now, RecvTS: now,
		Quality: 1, M: sensor.Measurement{Temperature: 32}}) // 32F == 0C

	snap := h.Snapshot()
	var temp *sensor.FusedResult
	for i := range snap.Fused {
		if snap.Fused[i].Kind == sensor.KindTemperature {
			temp = &snap.Fused[i]
		}
	}
	if temp == nil {
		t.Fatal("no temperature fusion")
	}
	if temp.M.Temperature > 1e-9 || temp.M.Temperature < -1e-9 {
		t.Fatalf("expected fused 0C, got %v", temp.M.Temperature)
	}
	if len(temp.Contribs) != 2 {
		t.Fatalf("expected 2 contributors, got %v", temp.Contribs)
	}
}

func TestRejectsDuplicateDevice(t *testing.T) {
	log := &captureLogger{}
	clk := clock.NewScriptedClock(nil)
	_, err := hub.New(hub.Config{Sources: []source.Descriptor{
		desc("dup", "temperature", "C", "thermal", "a1"),
		desc("dup", "temperature", "C", "thermal", "a2"),
	}}, clk, log)
	if !errors.Is(err, sensor.ErrDuplicateSource) {
		t.Fatalf("expected ErrDuplicateSource, got %v", err)
	}
}

func TestRejectsUnsupportedUnit(t *testing.T) {
	log := &captureLogger{}
	clk := clock.NewScriptedClock(nil)
	_, err := hub.New(hub.Config{Sources: []source.Descriptor{
		desc("t1", "temperature", "Rankine", "thermal", "a1"),
	}}, clk, log)
	if !errors.Is(err, sensor.ErrUnsupportedUnit) {
		t.Fatalf("expected ErrUnsupportedUnit, got %v", err)
	}
}

func TestRejectsKindUnitFrameMismatch(t *testing.T) {
	// GPS LLA with an ECEF frame is internally inconsistent.
	log := &captureLogger{}
	clk := clock.NewScriptedClock(nil)
	_, err := hub.New(hub.Config{Sources: []source.Descriptor{
		desc("g1", "gps", "lat,lon,alt", "ecef", "a1"),
	}}, clk, log)
	if !errors.Is(err, sensor.ErrConfigConflict) {
		t.Fatalf("expected ErrConfigConflict, got %v", err)
	}
}

func TestRejectsMixedGPSVariants(t *testing.T) {
	// LLA/WGS84 and ECEF sources must never be silently fused.
	log := &captureLogger{}
	clk := clock.NewScriptedClock(nil)
	_, err := hub.New(hub.Config{Sources: []source.Descriptor{
		desc("g1", "gps", "lat,lon,alt", "wgs84", "a1"),
		desc("g2", "gps", "ecef", "ecef", "a2"),
	}}, clk, log)
	if !errors.Is(err, sensor.ErrConfigConflict) {
		t.Fatalf("expected ErrConfigConflict for mixed GPS variants, got %v", err)
	}
}

func TestRejectsAddressCollision(t *testing.T) {
	log := &captureLogger{}
	clk := clock.NewScriptedClock(nil)
	_, err := hub.New(hub.Config{Sources: []source.Descriptor{
		desc("t1", "temperature", "C", "thermal", "bus:0"),
		desc("t2", "temperature", "F", "thermal", "bus:0"),
	}}, clk, log)
	if !errors.Is(err, sensor.ErrConfigConflict) {
		t.Fatalf("expected ErrConfigConflict for address collision, got %v", err)
	}
}

func TestDynamicAddSourceValidation(t *testing.T) {
	h, _ := newTestHub(t, []source.Descriptor{desc("t1", "temperature", "C", "thermal", "a1")})

	// duplicate
	if err := h.AddSource(desc("t1", "temperature", "F", "thermal", "a9")); !errors.Is(err, sensor.ErrDuplicateSource) {
		t.Fatalf("dup add: %v", err)
	}
	// conflicting canonical pair with existing kind
	if err := h.AddSource(desc("g1", "gps", "lat,lon,alt", "wgs84", "a2")); err != nil {
		t.Fatalf("gps should be fine as a new kind: %v", err)
	}
	if err := h.AddSource(desc("g2", "gps", "ecef", "ecef", "a3")); !errors.Is(err, sensor.ErrConfigConflict) {
		t.Fatalf("mixed gps variant: %v", err)
	}
	// unknown source ingest rejected
	err := h.Ingest(sensor.Sample{Source: "ghost", Seq: 1, RecvTS: time.Now()})
	if !errors.Is(err, sensor.ErrUnknownSource) {
		t.Fatalf("unknown source ingest: %v", err)
	}
}

func mustIngest(t *testing.T, h *hub.Hub, s sensor.Sample) {
	t.Helper()
	if s.SampleTS.IsZero() {
		s.SampleTS = s.RecvTS
	}
	if err := h.Ingest(s); err != nil {
		t.Fatalf("ingest %s seq=%d: %v", s.Source, s.Seq, err)
	}
}
