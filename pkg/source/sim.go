package source

import (
	"context"
	"errors"
	"sync"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/sensor"
)

// ErrDeviceFailed is emitted by a simulated source when the device enters
// its failed state.
var ErrDeviceFailed = errors.New("source: simulated device failure")

// SimSource is a deterministic simulated device. It can run a scripted
// timeline autonomously or be driven sample-by-sample via Inject.
//
// The same Options always produce the same stream: there is no jitter and
// no wall-clock dependency beyond the injected Clock used to stamp receive
// timestamps.
type SimSource struct {
	opts Options

	mu      sync.Mutex
	epoch   uint64
	failed  bool
	running bool

	inject  chan sensor.Sample
	restart chan struct{}
	fail    chan struct{}
}

// NewSimSource constructs a simulated source from options.
func NewSimSource(opts Options) (*SimSource, error) {
	if opts.Desc.ID == "" {
		return nil, sensor.ErrBadConfig
	}
	if err := sensor.ValidateRegistration(opts.Desc.Kind, opts.Desc.Unit, opts.Desc.Frame); err != nil {
		return nil, err
	}
	if opts.Clock == nil {
		return nil, sensor.ErrBadConfig
	}
	return &SimSource{
		opts:    opts,
		inject:  make(chan sensor.Sample, 64),
		restart: make(chan struct{}, 1),
		fail:    make(chan struct{}, 1),
	}, nil
}

// ID returns the source identifier.
func (s *SimSource) ID() sensor.SourceID { return s.opts.Desc.ID }

// Descriptor returns the static descriptor.
func (s *SimSource) Descriptor() Descriptor { return s.opts.Desc }

// Run starts scripted emission. The returned sample channel is closed when
// the context is canceled; the error channel carries device-failure
// notifications and is closed at the end.
func (s *SimSource) Run(ctx context.Context) (<-chan sensor.Sample, <-chan error) {
	out := make(chan sensor.Sample, 16)
	errs := make(chan error, 8)

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	// The feeder hands scripted entries to the main loop one at a time with
	// no timing randomness, so backpressure alone paces the stream.
	feed := make(chan ScriptedSample)
	go func() {
		defer close(feed)
		for _, e := range s.opts.Script {
			select {
			case <-ctx.Done():
				return
			case feed <- e:
			}
		}
	}()

	go func() {
		defer close(out)
		defer close(errs)
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case sa := <-s.inject:
				s.stampRecv(&sa)
				select {
				case out <- sa:
				case <-ctx.Done():
					return
				}
			case <-s.fail:
				select {
				case errs <- ErrDeviceFailed:
				default:
				}
				if !s.waitRestart(ctx) {
					return
				}
			case e, ok := <-feed:
				if !ok {
					// Script exhausted; keep serving injections until stop.
					select {
					case <-ctx.Done():
						return
					case sa := <-s.inject:
						s.stampRecv(&sa)
						select {
						case out <- sa:
						case <-ctx.Done():
							return
						}
					case <-s.fail:
						select {
						case errs <- ErrDeviceFailed:
						default:
						}
						if !s.waitRestart(ctx) {
							return
						}
					}
					continue
				}
				sa := s.build(e)
				select {
				case out <- sa:
				case <-ctx.Done():
					return
				}
				if e.RestartAfter {
					s.mu.Lock()
					s.epoch++
					s.mu.Unlock()
				}
				if e.FailAfter {
					s.mu.Lock()
					s.failed = true
					s.mu.Unlock()
					select {
					case errs <- ErrDeviceFailed:
					default:
					}
					if !s.waitRestart(ctx) {
						return
					}
				}
				if e.GapAfter {
					// Simulate loss: discard the next scripted entry without
					// ever emitting it. The sequence gap is visible to the
					// engine.
					if !s.discardOne(ctx, feed, out) {
						return
					}
				}
			}
		}
	}()

	return out, errs
}

// waitRestart blocks until the device is restarted or ctx ends. It bumps
// the epoch on restart so the post-restart stream is a new timeline.
func (s *SimSource) waitRestart(ctx context.Context) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-s.restart:
			return true
		}
	}
}

// discardOne drops exactly one scripted entry while still forwarding any
// externally injected samples that arrive in the meantime.
func (s *SimSource) discardOne(ctx context.Context, feed <-chan ScriptedSample, out chan<- sensor.Sample) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case sa := <-s.inject:
			s.stampRecv(&sa)
			select {
			case out <- sa:
			case <-ctx.Done():
				return false
			}
		case <-feed:
			return true
		}
	}
}

func (s *SimSource) build(e ScriptedSample) sensor.Sample {
	now := s.opts.Clock.Now()
	ts := e.SampleTS
	if ts.IsZero() {
		ts = now
	}
	q := e.Quality
	if q == 0 {
		q = 1.0
	}
	return sensor.Sample{
		Source:   s.opts.Desc.ID,
		Kind:     s.opts.Desc.Kind,
		Unit:     s.opts.Desc.Unit,
		Frame:    s.opts.Desc.Frame,
		Seq:      e.Seq,
		Epoch:    e.Epoch,
		SampleTS: ts,
		RecvTS:   now,
		Quality:  q,
		M:        e.M,
	}
}

func (s *SimSource) stampRecv(sa *sensor.Sample) {
	if sa.RecvTS.IsZero() {
		sa.RecvTS = s.opts.Clock.Now()
	}
	if sa.Source == "" {
		sa.Source = s.opts.Desc.ID
	}
	if sa.Kind == "" {
		sa.Kind = s.opts.Desc.Kind
		sa.Unit = s.opts.Desc.Unit
		sa.Frame = s.opts.Desc.Frame
	}
}

// Inject queues one externally-scripted sample for emission.
func (s *SimSource) Inject(sa sensor.Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// While failed, injections are refused so callers observe the outage
	// deterministically instead of silently buffering.
	if s.failed {
		return ErrDeviceFailed
	}
	s.inject <- sa
	return nil
}

// Fail marks the device failed. A running stream emits ErrDeviceFailed and
// pauses until Restart.
func (s *SimSource) Fail() {
	s.mu.Lock()
	already := s.failed
	s.failed = true
	s.mu.Unlock()
	if !already {
		select {
		case s.fail <- struct{}{}:
		default:
		}
	}
}

// Restart clears the failure and bumps the epoch.
func (s *SimSource) Restart() {
	s.mu.Lock()
	wasFailed := s.failed
	s.failed = false
	s.epoch++
	s.mu.Unlock()
	if wasFailed {
		select {
		case s.restart <- struct{}{}:
		default:
		}
	}
}

// Err reports the current failure.
func (s *SimSource) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return ErrDeviceFailed
	}
	return nil
}

// Epoch reports the current simulated boot epoch.
func (s *SimSource) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}
