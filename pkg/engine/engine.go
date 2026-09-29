// Package engine implements per-source ingest state machines
// (ordering, gaps, late arrival, restart, freshness) and the weighted
// fusion of the freshest valid samples.
package engine

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/calibrate"
	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

// SourceDescriptor is the static info the engine needs per source.
type SourceDescriptor struct {
	Kind  sensor.Kind
	Unit  sensor.Unit
	Frame sensor.Frame
}

// SourceStateMachine tracks the timeline of one source.
//
// All decisions are derived solely from the observed (Seq, Epoch,
// SampleTS, RecvTS, Quality) tuple and the clock time given to Evaluate,
// so an identical input script always produces identical state.
type SourceStateMachine struct {
	id     sensor.SourceID
	kind   sensor.Kind
	tuning Tuning

	mu sync.Mutex

	initialised bool
	epoch       uint64
	lastSeq     uint64
	lastTS      time.Time
	lastRecv    time.Time

	latest    sensor.NormalizedSample
	latestCal sensor.Measurement
	hasLatest bool

	gapCount  int
	lateCount int
	oooCount  int

	lastStatus sensor.Status
	lastReason sensor.Reason

	failed bool
}

// NewSourceStateMachine constructs a state machine for a descriptor.
func NewSourceStateMachine(id sensor.SourceID, d SourceDescriptor, tuning Tuning) *SourceStateMachine {
	return &SourceStateMachine{
		id:         id,
		kind:       d.Kind,
		tuning:     tuning,
		lastStatus: sensor.StatusUnknown,
	}
}

// IngestResult reports how one sample was handled.
type IngestResult struct {
	Accepted bool
	Status   sensor.Status
	Reason   sensor.Reason
	Epoch    uint64
	Seq      uint64
}

// Observe feeds one normalized (and calibration-tagged) sample into the
// state machine, enforcing deterministic rules:
//
//   - epoch bump  -> restart; old-epoch samples are rejected as stale.
//   - seq/ts jump -> gap counted; seq/ts regression -> out-of-order.
//   - arrival later than MaxLate after sample time -> late (rejected).
func (sm *SourceStateMachine) Observe(ns sensor.NormalizedSample) IngestResult {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	res := IngestResult{Epoch: ns.Epoch, Seq: ns.Seq}

	// Late arrival: received more than MaxLate after it was sampled.
	// Such data can never be "the latest" and is rejected outright.
	if !ns.RecvTS.IsZero() && !ns.SampleTS.IsZero() &&
		ns.RecvTS.Sub(ns.SampleTS) > sm.tuning.maxLate() {
		sm.lateCount++
		res.Status = sm.lastStatus
		res.Reason = sensor.ReasonLate
		return res
	}

	if !sm.initialised {
		sm.initialised = true
		sm.epoch = ns.Epoch
		sm.lastSeq = ns.Seq
		sm.lastTS = ns.SampleTS
		sm.lastRecv = ns.RecvTS
		sm.latest = ns
		sm.hasLatest = true
		sm.lastStatus = sensor.StatusOnline
		sm.lastReason = sensor.ReasonNone
		res.Accepted = true
		res.Status = sensor.StatusOnline
		return res
	}

	switch {
	case ns.Epoch < sm.epoch:
		// Data from before a restart that we already observed.
		res.Status = sm.lastStatus
		res.Reason = sensor.ReasonExpired
		return res
	case ns.Epoch > sm.epoch:
		// Restart: the device rebooted and its sequence counter restarted.
		// The new timeline supersedes the old one: old latest data is
		// dropped so expired samples never win, and any previous device
		// failure is considered recovered.
		sm.failed = false
		sm.epoch = ns.Epoch
		sm.lastSeq = ns.Seq
		sm.lastTS = ns.SampleTS
		sm.lastRecv = ns.RecvTS
		sm.latest = ns
		sm.hasLatest = true
		sm.lastStatus = sensor.StatusRestarted
		sm.lastReason = sensor.ReasonEpochBump
		res.Accepted = true
		res.Status = sensor.StatusRestarted
		res.Reason = sensor.ReasonEpochBump
		return res
	}

	// Same epoch: enforce monotonic (Seq, SampleTS).
	switch {
	case ns.Seq < sm.lastSeq || (!ns.SampleTS.IsZero() && ns.SampleTS.Before(sm.lastTS)):
		sm.oooCount++
		res.Status = sm.lastStatus
		res.Reason = sensor.ReasonOutOfOrder
		return res
	case ns.Seq > sm.lastSeq+1:
		sm.gapCount += int(ns.Seq - sm.lastSeq - 1)
	}

	sm.lastSeq = ns.Seq
	sm.lastTS = ns.SampleTS
	sm.lastRecv = ns.RecvTS
	sm.latest = ns
	sm.hasLatest = true
	sm.lastStatus = sensor.StatusOnline
	sm.lastReason = sensor.ReasonNone
	res.Accepted = true
	res.Status = sensor.StatusOnline
	return res
}

// Evaluate recomputes health (online/degraded/stale/offline/restarted)
// against now and the configured windows. Restarted latches until the
// freshness evaluation after the first post-restart sample proves the
// timeline is flowing again.
func (sm *SourceStateMachine) Evaluate(now time.Time) sensor.SourceState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	st := sensor.SourceState{
		ID:        sm.id,
		Kind:      sm.kind,
		LastSeq:   sm.lastSeq,
		LastTS:    sm.lastTS,
		LastRecv:  sm.lastRecv,
		Epoch:     sm.epoch,
		GapCount:  sm.gapCount,
		LateCount: sm.lateCount,
	}

	if !sm.initialised {
		st.Status = sensor.StatusUnknown
		return st
	}
	if sm.failed {
		st.Status = sensor.StatusOffline
		st.Reason = sensor.Reason("source_error")
		return st
	}

	fresh := sm.tuning.freshness(sm.kind)
	age := now.Sub(sm.lastRecv)

	switch {
	case sm.lastStatus == sensor.StatusRestarted:
		st.Status = sensor.StatusRestarted
		st.Reason = sensor.ReasonEpochBump
		// one healthy evaluation clears the latch
		if age <= fresh {
			sm.lastStatus = sensor.StatusOnline
			sm.lastReason = sensor.ReasonNone
		}
	case age > sm.tuning.offlineAfter():
		st.Status = sensor.StatusOffline
		st.Reason = sensor.ReasonNoFreshData
	case sm.hasLatest && sm.latest.Quality < sm.tuning.qualityFloor():
		st.Status = sensor.StatusDegraded
		st.Reason = sensor.ReasonQualityLow
	case age > fresh:
		st.Status = sensor.StatusStale
		st.Reason = sensor.ReasonNoFreshData
	default:
		st.Status = sensor.StatusOnline
		st.Reason = sensor.ReasonNone
	}
	return st
}

// Latest returns the latest accepted normalized sample and its calibrated
// measurement.
func (sm *SourceStateMachine) Latest() (sensor.NormalizedSample, sensor.Measurement, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.hasLatest {
		return sensor.NormalizedSample{}, sensor.Measurement{}, false
	}
	return sm.latest, sm.latestCal, true
}

// SetCalibrated stores the calibrated form of the latest sample.
func (sm *SourceStateMachine) SetCalibrated(m sensor.Measurement) {
	sm.mu.Lock()
	sm.latestCal = m
	sm.mu.Unlock()
}

// PromoteLatest re-tags the latest sample to a new calibration epoch and
// re-calibrates its measurement with the new parameters. It is called when
// a calibration update happens: the most recent observation is
// immediately expressed in the new epoch, while older observations are not
// retained at all, so no old/new parameter mixture is possible.
func (sm *SourceStateMachine) PromoteLatest(epoch uint64, cal func(sensor.NormalizedSample) sensor.Measurement) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.hasLatest {
		return false
	}
	sm.latest.CalEpoch = epoch
	sm.latestCal = cal(sm.latest)
	return true
}

// CalEpoch returns the calibration epoch currently tagged on the latest.
func (sm *SourceStateMachine) CalEpoch() (uint64, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.hasLatest {
		return 0, false
	}
	return sm.latest.CalEpoch, true
}

// MarkFailed records an observed device failure. The source is excluded
// from fusion until it recovers, which happens automatically when a
// sample from a newer epoch is observed (a restart).
func (sm *SourceStateMachine) MarkFailed() {
	sm.mu.Lock()
	sm.failed = true
	sm.mu.Unlock()
}

// MarkRecovered clears a previously recorded failure.
func (sm *SourceStateMachine) MarkRecovered() {
	sm.mu.Lock()
	sm.failed = false
	sm.mu.Unlock()
}

// FusibleWeight returns the fusion weight of the current latest sample at
// time now, or 0 if the sample must not participate (offline, stale,
// restarted-but-unproven, or below quality floor).
func (sm *SourceStateMachine) FusibleWeight(now time.Time) float64 {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.hasLatest || sm.failed {
		return 0
	}
	age := now.Sub(sm.lastRecv)
	if age > sm.tuning.freshness(sm.kind) {
		return 0
	}
	if sm.lastStatus == sensor.StatusRestarted {
		return 0
	}
	q := sm.latest.Quality
	if q < sm.tuning.qualityFloor() {
		return 0
	}
	if q <= 0 {
		q = 1
	}
	// Linear freshness decay inside the window; deterministic, no randomness.
	f := 1.0 - age.Seconds()/sm.tuning.freshness(sm.kind).Seconds()
	if f < 0 {
		f = 0
	}
	w := q * f
	if w <= 0 {
		return 0
	}
	return w
}

// Tuning holds the deterministic timing policy.
type Tuning struct {
	Freshness    map[sensor.Kind]time.Duration
	DefaultFresh time.Duration
	OfflineAfter time.Duration
	MaxLate      time.Duration
	QualityFloor float64
}

func (t Tuning) freshness(k sensor.Kind) time.Duration {
	if d, ok := t.Freshness[k]; ok && d > 0 {
		return d
	}
	if t.DefaultFresh > 0 {
		return t.DefaultFresh
	}
	return sensor.FreshnessWindow(k)
}

func (t Tuning) offlineAfter() time.Duration {
	if t.OfflineAfter > 0 {
		return t.OfflineAfter
	}
	return 5 * time.Second
}

func (t Tuning) maxLate() time.Duration {
	if t.MaxLate > 0 {
		return t.MaxLate
	}
	return 2 * time.Second
}

func (t Tuning) qualityFloor() float64 {
	if t.QualityFloor > 0 {
		return t.QualityFloor
	}
	return 0.3
}

// DefaultTuning returns the policy used by local runs.
func DefaultTuning() Tuning {
	return Tuning{
		Freshness: map[sensor.Kind]time.Duration{
			sensor.KindTemperature: 2 * time.Second,
			sensor.KindIMU:         time.Second,
			sensor.KindGPS:         2 * time.Second,
		},
		DefaultFresh: time.Second,
		OfflineAfter: 5 * time.Second,
		MaxLate:      2 * time.Second,
		QualityFloor: 0.3,
	}
}

// Engine owns the per-source state machines and performs fusion.
type Engine struct {
	reg    *calibrate.Registry
	tuning Tuning

	mu      sync.RWMutex
	sources map[sensor.SourceID]*SourceStateMachine
	descs   map[sensor.SourceID]SourceDescriptor
	order   []sensor.SourceID // stable registration order for reproducibility
}

// New constructs an engine bound to a calibration registry.
func New(reg *calibrate.Registry, tuning Tuning) *Engine {
	return &Engine{
		reg:     reg,
		tuning:  tuning,
		sources: map[sensor.SourceID]*SourceStateMachine{},
		descs:   map[sensor.SourceID]SourceDescriptor{},
	}
}

// AddSource registers a source state machine.
func (e *Engine) AddSource(id sensor.SourceID, d SourceDescriptor) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.sources[id]; ok {
		return sensor.ErrDuplicateSource
	}
	e.sources[id] = NewSourceStateMachine(id, d, e.tuning)
	e.descs[id] = d
	e.order = append(e.order, id)
	return nil
}

// MarkSourceFailed forwards a device failure to the source state machine.
func (e *Engine) MarkSourceFailed(id sensor.SourceID) {
	e.mu.RLock()
	sm, ok := e.sources[id]
	e.mu.RUnlock()
	if ok {
		sm.MarkFailed()
	}
}

// Ingest routes one normalized sample to its state machine, calibrates it
// with the parameters of the epoch tagged on the sample, and stores the
// calibrated measurement.
func (e *Engine) Ingest(ns sensor.NormalizedSample) (IngestResult, error) {
	e.mu.RLock()
	sm, ok := e.sources[ns.Source]
	e.mu.RUnlock()
	if !ok {
		return IngestResult{}, sensor.ErrUnknownSource
	}
	res := sm.Observe(ns)
	if res.Accepted {
		p, found := e.reg.ParamsAt(ns.Source, ns.CalEpoch)
		if !found {
			p = calibrate.IdentityParams()
		}
		sm.SetCalibrated(calibrate.Apply(p, ns.Kind, ns.M))
	}
	return res, nil
}

// PromoteSource re-tags a source's latest sample to the registry's current
// calibration epoch and re-calibrates it.
func (e *Engine) PromoteSource(id sensor.SourceID) bool {
	e.mu.RLock()
	sm, ok := e.sources[id]
	e.mu.RUnlock()
	if !ok {
		return false
	}
	epoch, ok := e.reg.CurrentEpoch(id)
	if !ok {
		return false
	}
	return sm.PromoteLatest(epoch, func(ns sensor.NormalizedSample) sensor.Measurement {
		p, found := e.reg.ParamsAt(id, ns.CalEpoch)
		if !found {
			p = calibrate.IdentityParams()
		}
		return calibrate.Apply(p, ns.Kind, ns.M)
	})
}

// Snapshot builds a consistent view: per-source states plus one fused
// result per kind, using only samples tagged with the latest calibration
// epoch of each source.
func (e *Engine) Snapshot(now time.Time, log sensor.Logger) sensor.Snapshot {
	e.mu.RLock()
	ids := make([]sensor.SourceID, len(e.order))
	copy(ids, e.order)
	sms := make([]*SourceStateMachine, len(ids))
	for i, id := range ids {
		sms[i] = e.sources[id]
	}
	e.mu.RUnlock()

	snap := sensor.Snapshot{TakenAt: now}
	byKind := map[sensor.Kind][]*SourceStateMachine{}
	for _, sm := range sms {
		st := sm.Evaluate(now)
		d := e.descs[sm.id]
		st.Unit = d.Unit
		st.Frame = d.Frame
		snap.Sources = append(snap.Sources, st)
		byKind[st.Kind] = append(byKind[st.Kind], sm)
	}

	kinds := make([]sensor.Kind, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	for _, k := range kinds {
		snap.Fused = append(snap.Fused, e.fuseKind(now, k, byKind[k], log))
	}
	return snap
}

// fuseKind computes one weighted-average fusion for a kind.
//
// Deterministic rules:
//   - only sources with positive fusion weight at `now` participate;
//   - only samples at each source's CURRENT calibration epoch participate,
//     so a calibration update never mixes old and new parameters;
//   - contributions are ordered by source ID for reproducible output;
//   - with a single contributor the result is DEGRADED (redundancy lost);
//     with none the result is STALE/offline-derived.
func (e *Engine) fuseKind(now time.Time, k sensor.Kind, sms []*SourceStateMachine, log sensor.Logger) sensor.FusedResult {
	type contrib struct {
		id    sensor.SourceID
		w     float64
		ns    sensor.NormalizedSample
		m     sensor.Measurement
		frame sensor.Frame
		epoch uint64
	}
	var all []contrib
	var good []contrib

	for _, sm := range sms {
		ns, cm, ok := sm.Latest()
		if !ok {
			continue
		}
		curEpoch, _ := e.reg.CurrentEpoch(sm.id)
		w := sm.FusibleWeight(now)
		c := contrib{
			id: sm.id, w: w, ns: ns, m: cm,
			frame: ns.Frame, epoch: ns.CalEpoch,
		}
		if w > 0 && ns.CalEpoch == curEpoch {
			good = append(good, c)
		}
		all = append(all, c)
	}
	sort.Slice(good, func(i, j int) bool { return good[i].id < good[j].id })

	res := sensor.FusedResult{Kind: k, Status: sensor.StatusStale}
	if len(good) == 0 {
		res.Reason = sensor.ReasonNoFreshData
		if log != nil {
			log.Warn("fusion: no eligible source", "kind", k)
		}
		return res
	}

	var wsum float64
	acc := sensor.Measurement{}
	frame := good[0].frame
	epoch := good[0].epoch
	for _, c := range good {
		wsum += c.w
		acc = addWeighted(acc, c.m, c.w, k)
		res.Contribs = append(res.Contribs, c.id)
	}
	res.M = scaleResult(acc, 1.0/wsum, k)
	res.Frame = frame
	res.CalEpoch = epoch
	res.Quality = math.Min(1, wsum/float64(len(good)))

	switch {
	case len(good) == 1:
		res.Status = sensor.StatusDegraded
		res.Reason = sensor.Reason("single_source")
	case len(good) < len(all):
		res.Status = sensor.StatusDegraded
		res.Reason = sensor.Reason("partial_sources")
	default:
		res.Status = sensor.StatusOnline
	}
	if log != nil {
		log.Info("fusion: result",
			"kind", k, "status", res.Status, "reason", res.Reason,
			"contributors", contributorNames(res.Contribs), "cal_epoch", res.CalEpoch)
	}
	return res
}

func contributorNames(ids []sensor.SourceID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

func addWeighted(acc, m sensor.Measurement, w float64, k sensor.Kind) sensor.Measurement {
	switch k {
	case sensor.KindTemperature:
		acc.Temperature += m.Temperature * w
	case sensor.KindIMU:
		acc.Gyro.X += m.Gyro.X * w
		acc.Gyro.Y += m.Gyro.Y * w
		acc.Gyro.Z += m.Gyro.Z * w
		acc.Acc.X += m.Acc.X * w
		acc.Acc.Y += m.Acc.Y * w
		acc.Acc.Z += m.Acc.Z * w
	case sensor.KindGPS:
		acc.Lat += m.Lat * w
		acc.Lon += m.Lon * w
		acc.Alt += m.Alt * w
		acc.X += m.X * w
		acc.Y += m.Y * w
		acc.Z += m.Z * w
	}
	return acc
}

func scaleResult(m sensor.Measurement, f float64, k sensor.Kind) sensor.Measurement {
	switch k {
	case sensor.KindTemperature:
		m.Temperature *= f
	case sensor.KindIMU:
		m.Gyro = sensor.Vec3{X: m.Gyro.X * f, Y: m.Gyro.Y * f, Z: m.Gyro.Z * f}
		m.Acc = sensor.Vec3{X: m.Acc.X * f, Y: m.Acc.Y * f, Z: m.Acc.Z * f}
	case sensor.KindGPS:
		m.Lat *= f
		m.Lon *= f
		m.Alt *= f
		m.X *= f
		m.Y *= f
		m.Z *= f
	}
	return m
}
