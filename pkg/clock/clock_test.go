package clock_test

import (
	"testing"
	"time"

	"github.com/h3yow/sensor-fusion-hub-go/pkg/clock"
)

func TestScriptedClockTimeline(t *testing.T) {
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	c := clock.NewScriptedClock([]clock.Step{
		{At: t0, Tag: "t0"},
		{At: t0.Add(time.Second), Tag: "t1"},
	})
	if !c.Now().Equal(t0) || c.At() != "t0" {
		t.Fatalf("initial: %v %q", c.Now(), c.At())
	}
	c.Advance()
	if !c.Now().Equal(t0.Add(time.Second)) || c.At() != "t1" {
		t.Fatalf("advance: %v %q", c.Now(), c.At())
	}
	c.Advance() // past end keeps last value
	if !c.Now().Equal(t0.Add(time.Second)) {
		t.Fatal("clock must hold last value past end")
	}
	c.Set(t0.Add(5 * time.Second))
	if !c.Now().Equal(t0.Add(5 * time.Second)) {
		t.Fatal("set failed")
	}
}

func TestScriptedClockConcurrentReads(t *testing.T) {
	c := clock.NewScriptedClock(nil)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			_ = c.Now()
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		c.Set(time.Unix(int64(i), 0))
	}
	<-done
}
