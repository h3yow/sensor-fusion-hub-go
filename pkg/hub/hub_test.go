package hub_test

import (
	"fmt"
	"strings"
	"sync"
)

// captureLogger records every log call so tests can assert that the input
// timeline, verdict basis and final status are all logged.
type entry struct {
	level string
	msg   string
	args  []any
}

type captureLogger struct {
	mu      sync.Mutex
	entries []entry
}

func (c *captureLogger) add(level, msg string, args []any) {
	c.mu.Lock()
	c.entries = append(c.entries, entry{level, msg, args})
	c.mu.Unlock()
}
func (c *captureLogger) Info(msg string, args ...any)          { c.add("info", msg, args) }
func (c *captureLogger) Warn(msg string, args ...any)          { c.add("warn", msg, args) }
func (c *captureLogger) Error(msg string, args ...any)         { c.add("error", msg, args) }
func (c *captureLogger) InputTimeline(msg string, args ...any) { c.add("timeline", msg, args) }

func (c *captureLogger) render() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, e := range c.entries {
		b.WriteString(e.level)
		b.WriteByte(' ')
		b.WriteString(e.msg)
		for i := 0; i+1 < len(e.args); i += 2 {
			b.WriteByte(' ')
			b.WriteString(fmt.Sprint(e.args[i]))
			b.WriteByte('=')
			b.WriteString(fmt.Sprint(e.args[i+1]))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// findArg returns the first value logged for key at the given level.
func (c *captureLogger) findArg(level, key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.level != level {
			continue
		}
		for i := 0; i+1 < len(e.args); i += 2 {
			if fmt.Sprint(e.args[i]) == key {
				return fmt.Sprint(e.args[i+1]), true
			}
		}
	}
	return "", false
}

// countLevel returns how many entries exist at the given level.
func (c *captureLogger) countLevel(level string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries {
		if e.level == level {
			n++
		}
	}
	return n
}
