// Package hub wires sources, the deterministic clock, calibration
// registry and fusion engine into a reusable local component.
package hub

import (
	"context"
	"sync"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/calibrate"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/clock"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/engine"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/source"
)

// Config is the declarative configuration of one hub instance.
type Config struct {
	Sources []source.Descriptor
	Tuning  engine.Tuning
}

// Hub is the entry point of the reusable component.
type Hub struct {
	clk  clock.Clock
	log  sensor.Logger
	reg  *calibrate.Registry
	eng  *engine.Engine
	tune engine.Tuning

	mu       sync.RWMutex
	descs    map[sensor.SourceID]source.Descriptor
	pairs    map[sensor.Kind]sensor.Unit // canonical unit per kind
	frames   map[sensor.Kind]sensor.Frame
	addrs    map[string]sensor.SourceID
	failures map[sensor.SourceID]error
}

// New validates the full configuration (duplicate IDs, kind/unit/frame
// support, same-kind compatibility, address collisions) and constructs a
// hub. Invalid configurations are rejected explicitly, never partially
// applied or silently overridden.
func New(cfg Config, clk clock.Clock, log sensor.Logger) (*Hub, error) {
	if clk == nil {
		return nil, sensor.ErrBadConfig
	}
	if log == nil {
		log = nopLogger{}
	}
	h := &Hub{
		clk:      clk,
		log:      log,
		reg:      calibrate.NewRegistry(),
		tune:     cfg.Tuning,
		descs:    map[sensor.SourceID]source.Descriptor{},
		pairs:    map[sensor.Kind]sensor.Unit{},
		frames:   map[sensor.Kind]sensor.Frame{},
		addrs:    map[string]sensor.SourceID{},
		failures: map[sensor.SourceID]error{},
	}
	h.eng = engine.New(h.reg, cfg.Tuning)

	// Validate everything first; only when the whole config is sound do we
	// mutate state. Conflicting sources never partially register.
	type prepared struct {
		d             source.Descriptor
		canonicalUnit sensor.Unit
		canonicalFr   sensor.Frame
	}
	var preparedList []prepared
	seenID := map[sensor.SourceID]bool{}
	localAddrs := map[string]sensor.SourceID{}

	for _, d := range cfg.Sources {
		if d.ID == "" || d.Kind == "" {
			return nil, sensor.ErrBadConfig
		}
		if seenID[d.ID] {
			return nil, sensor.ErrDuplicateSource
		}
		seenID[d.ID] = true

		cu, cf, err := sensor.NormalizedUnitPair(d.Kind, d.Unit, d.Frame)
		if err != nil {
			return nil, err
		}
		if existing, ok := localAddrs[d.Address]; ok && d.Address != "" && existing != d.ID {
			return nil, sensor.ErrConfigConflict
		}
		localAddrs[d.Address] = d.ID
		preparedList = append(preparedList, prepared{d, cu, cf})
	}

	// Same-kind fusability: canonical unit AND frame must match across all
	// sources of that kind. Different variants (e.g. GPS LLA vs ECEF) are
	// rejected instead of being silently mixed.
	pendingUnits := map[sensor.Kind]sensor.Unit{}
	pendingFrames := map[sensor.Kind]sensor.Frame{}
	for _, p := range preparedList {
		if u, ok := pendingUnits[p.d.Kind]; ok && u != p.canonicalUnit {
			return nil, sensor.ErrConfigConflict
		}
		if f, ok := pendingFrames[p.d.Kind]; ok && f != p.canonicalFr {
			return nil, sensor.ErrConfigConflict
		}
		pendingUnits[p.d.Kind] = p.canonicalUnit
		pendingFrames[p.d.Kind] = p.canonicalFr
	}

	for _, p := range preparedList {
		if err := h.applyRegistration(p.d, p.canonicalUnit, p.canonicalFr); err != nil {
			return nil, err
		}
	}
	h.log.Info("hub: configuration accepted", "sources", len(cfg.Sources))
	return h, nil
}

func (h *Hub) applyRegistration(d source.Descriptor, cu sensor.Unit, cf sensor.Frame) error {
	if err := h.reg.Register(d.ID, d.Kind); err != nil {
		return err
	}
	if err := h.eng.AddSource(d.ID, engine.SourceDescriptor{
		Kind: d.Kind, Unit: cu, Frame: cf,
	}); err != nil {
		return err
	}
	h.descs[d.ID] = d
	h.pairs[d.Kind] = cu
	h.frames[d.Kind] = cf
	if d.Address != "" {
		h.addrs[d.Address] = d.ID
	}
	return nil
}

// AddSource dynamically registers an additional source with the same
// strict validation as at construction time.
func (h *Hub) AddSource(d source.Descriptor) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if d.ID == "" || d.Kind == "" {
		return sensor.ErrBadConfig
	}
	if _, exists := h.descs[d.ID]; exists {
		return sensor.ErrDuplicateSource
	}
	cu, cf, err := sensor.NormalizedUnitPair(d.Kind, d.Unit, d.Frame)
	if err != nil {
		return err
	}
	if owner, ok := h.addrs[d.Address]; ok && d.Address != "" && owner != d.ID {
		return sensor.ErrConfigConflict
	}
	if u, ok := h.pairs[d.Kind]; ok && u != cu {
		return sensor.ErrConfigConflict
	}
	if f, ok := h.frames[d.Kind]; ok && f != cf {
		return sensor.ErrConfigConflict
	}
	if err := h.applyRegistration(d, cu, cf); err != nil {
		return err
	}
	h.log.Info("hub: source added", "id", d.ID, "kind", d.Kind,
		"unit", d.Unit, "frame", d.Frame)
	return nil
}

// Attach starts consuming from an already-validated source. A failing
// source is logged and recorded but never tears down other sources: every
// source runs in its own goroutine set.
func (h *Hub) Attach(ctx context.Context, src source.Source) error {
	d := src.Descriptor()
	h.mu.RLock()
	_, known := h.descs[d.ID]
	h.mu.RUnlock()
	if !known {
		return sensor.ErrUnknownSource
	}
	samples, errs := src.Run(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sa, ok := <-samples:
				if !ok {
					return
				}
				if err := h.Ingest(sa); err != nil {
					h.log.Warn("hub: ingest rejected",
						"id", sa.Source, "seq", sa.Seq, "epoch", sa.Epoch,
						"sample_ts", sa.SampleTS.Format(time.RFC3339Nano),
						"recv_ts", sa.RecvTS.Format(time.RFC3339Nano),
						"err", err.Error())
				}
			case err, ok := <-errs:
				if !ok {
					continue
				}
				h.mu.Lock()
				h.failures[d.ID] = err
				h.mu.Unlock()
				h.eng.MarkSourceFailed(d.ID)
				h.log.Error("hub: source failure", "id", d.ID, "err", err.Error())
			}
		}
	}()
	h.log.Info("hub: source attached", "id", d.ID)
	return nil
}

// Ingest feeds one raw sample (used by Attach and for deterministic
// replay/injection). It normalizes units/frames and tags the sample with
// the calibration epoch currently active for its source, then hands it to
// the engine state machine.
func (h *Hub) Ingest(raw sensor.Sample) error {
	h.mu.RLock()
	d, known := h.descs[raw.Source]
	h.mu.RUnlock()
	if !known {
		return sensor.ErrUnknownSource
	}
	if raw.Kind == "" {
		raw.Kind = d.Kind
		raw.Unit = d.Unit
		raw.Frame = d.Frame
	}
	if raw.RecvTS.IsZero() {
		raw.RecvTS = h.clk.Now()
	}

	ns := sensor.Normalize(raw)
	epoch, ok := h.reg.CurrentEpoch(raw.Source)
	if !ok {
		return sensor.ErrUnknownSource
	}
	ns.CalEpoch = epoch

	res, err := h.eng.Ingest(ns)
	if err != nil {
		return err
	}
	h.log.InputTimeline("ingest",
		"id", raw.Source, "seq", raw.Seq, "epoch", raw.Epoch,
		"cal_epoch", epoch,
		"sample_ts", raw.SampleTS.Format(time.RFC3339Nano),
		"recv_ts", raw.RecvTS.Format(time.RFC3339Nano),
		"accepted", res.Accepted, "verdict_status", res.Status,
		"verdict_reason", res.Reason, "quality", raw.Quality)
	if !res.Accepted {
		switch res.Reason {
		case sensor.ReasonOutOfOrder:
			return sensor.ErrOutOfOrder
		case sensor.ReasonExpired:
			return sensor.ErrStale
		case sensor.ReasonLate:
			return sensor.ErrLateArrival
		}
	}
	return nil
}

// UpdateCalibration validates and applies a new calibration revision.
// Already-ingested samples keep their old epoch tag and fusion after the
// update uses only samples at the new epoch, so old/new data never
// cross-compute.
func (h *Hub) UpdateCalibration(id sensor.SourceID, p calibrate.Params) (uint64, error) {
	h.mu.Lock()
	_, known := h.descs[id]
	h.mu.Unlock()
	if !known {
		return 0, sensor.ErrUnknownSource
	}
	epoch, err := h.reg.Update(id, p)
	if err != nil {
		return 0, err
	}
	// Immediately move the freshest observation onto the new epoch. Older
	// observations are not retained, so old and new parameters can never
	// be fused together.
	h.eng.PromoteSource(id)
	h.log.Info("hub: calibration updated", "id", id, "new_epoch", epoch)
	return epoch, nil
}

// MarkSourceFailed records an observed device failure (for example when an
// externally supervised source reports an error outside its stream). The
// source is excluded from fusion until a newer-epoch sample (restart)
// arrives.
func (h *Hub) MarkSourceFailed(id sensor.SourceID) {
	h.mu.Lock()
	h.failures[id] = source.ErrDeviceFailed
	h.mu.Unlock()
	h.eng.MarkSourceFailed(id)
	h.log.Error("hub: source marked failed", "id", id)
}

// Snapshot returns the current consistent view. All states and fused
// results inside one snapshot are evaluated against the same clock read,
// under a single read lock.
func (h *Hub) Snapshot() sensor.Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	now := h.clk.Now()
	snap := h.eng.Snapshot(now, h.log)
	for i := range snap.Sources {
		if err := h.failures[snap.Sources[i].ID]; err != nil {
			if snap.Sources[i].Status == sensor.StatusOnline {
				snap.Sources[i].Status = sensor.StatusDegraded
				snap.Sources[i].Reason = sensor.Reason("source_error")
			}
		}
	}
	return snap
}

// Run evaluates housekeeping periodically until ctx is canceled. It is
// optional: Snapshot always evaluates on demand.
func (h *Hub) Run(ctx context.Context) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_ = h.Snapshot()
		}
	}
}

// Clock returns the bound clock.
func (h *Hub) Clock() clock.Clock { return h.clk }

// SetMaxStaleness overrides the staleness threshold for all kinds.
func (h *Hub) SetMaxStaleness(d time.Duration) {
	h.mu.Lock()
	h.tune.DefaultFresh = d
	h.mu.Unlock()
}

type nopLogger struct{}

func (nopLogger) Info(string, ...any)          {}
func (nopLogger) Warn(string, ...any)          {}
func (nopLogger) Error(string, ...any)         {}
func (nopLogger) InputTimeline(string, ...any) {}
