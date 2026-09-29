// Package clock provides a controllable clock so that acquisition,
// freshness evaluation and replay tests are deterministic.
package clock

import (
	"sync"
	"time"
)

// Clock is the minimal time source used by the hub.
type Clock interface {
	Now() time.Time
}

// WallClock reports the real system time.
type WallClock struct{}

// NewWallClock constructs a wall-clock time source.
func NewWallClock() *WallClock { return &WallClock{} }

// Now returns the current system time.
func (WallClock) Now() time.Time { return time.Now() }

// Step is one entry of a scripted clock timeline.
type Step struct {
	At  time.Time
	Tag string
}

// ScriptedClock replays a fixed timeline; Now always reports the value set
// by the most recent Advance/Set call, making tests fully reproducible.
//
// It is safe for concurrent use: source goroutines may call Now while a
// test driver advances time. The clock itself contains no randomness and
// no timer-driven movement, so the same script always yields the same
// timeline.
type ScriptedClock struct {
	mu    sync.RWMutex
	steps []Step
	idx   int
	cur   time.Time
	tag   string
}

// NewScriptedClock builds a clock over an ordered timeline.
func NewScriptedClock(steps []Step) *ScriptedClock {
	c := &ScriptedClock{steps: steps}
	if len(steps) > 0 {
		c.cur = steps[0].At
		c.tag = steps[0].Tag
		c.idx = 0
	}
	return c
}

// Start moves to the first timeline entry.
func (c *ScriptedClock) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.steps) > 0 {
		c.idx = 0
		c.cur = c.steps[0].At
		c.tag = c.steps[0].Tag
	}
}

// Advance moves to the next timeline entry; at the end it keeps the last
// value.
func (c *ScriptedClock) Advance() Step {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx+1 < len(c.steps) {
		c.idx++
		c.cur = c.steps[c.idx].At
		c.tag = c.steps[c.idx].Tag
	}
	return c.steps[c.idx]
}

// HasMore reports whether Advance will move to a new entry.
func (c *ScriptedClock) HasMore() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.idx+1 < len(c.steps)
}

// Set pins the clock to an arbitrary instant and clears the tag.
func (c *ScriptedClock) Set(t time.Time) {
	c.mu.Lock()
	c.cur = t
	c.tag = ""
	c.mu.Unlock()
}

// Now reports the current scripted time.
func (c *ScriptedClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cur
}

// At returns the current step's tag (for logging the input timeline).
func (c *ScriptedClock) At() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tag
}

// Index returns the current step index.
func (c *ScriptedClock) Index() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.idx
}
