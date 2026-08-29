package supervisor

import (
	"strings"
	"sync"
)

// trigger identifies why a supervised ffmpeg stopped.
type trigger int

const (
	exit     trigger = iota // the process exited on its own
	manual                  // a manual restart was requested
	shutdown                // the daemon is stopping
)

// ringLog is a capped, thread-safe byte buffer that keeps the tail of a
// process' output. The daemon keeps one per platform so it can remember the
// last error without a journal.
type ringLog struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func newRingLog(max int) *ringLog { return &ringLog{max: max} }

// Write implements io.Writer.
func (r *ringLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.b = append(r.b, p...)
	if len(r.b) > r.max {
		r.b = r.b[len(r.b)-r.max:]
	}
	return len(p), nil
}

// reset clears the buffer (called when a new process starts).
func (r *ringLog) reset() {
	r.mu.Lock()
	r.b = r.b[:0]
	r.mu.Unlock()
}

// lastLine returns the last non-empty line, truncated to a displayable
// length.
func (r *ringLog) lastLine() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := strings.Split(string(r.b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return truncateLine(l)
		}
	}
	return ""
}

func truncateLine(s string) string {
	if len(s) > 200 {
		return "..." + s[len(s)-197:]
	}
	return s
}
