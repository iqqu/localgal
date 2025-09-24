package gui

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

type logBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

func newLogBuffer(cap int) *logBuffer {
	if cap < 1 {
		cap = 30
	}
	return &logBuffer{cap: cap, lines: make([]string, 0, cap)}
}

func (b *logBuffer) append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if line == "" {
		return
	}
	// split incoming by newlines to ensure discrete lines
	for _, part := range strings.Split(line, "\n") {
		p := strings.TrimRight(part, "\r")
		if p == "" {
			continue
		}
		b.lines = append(b.lines, p)
		if len(b.lines) > b.cap {
			// drop oldest
			b.lines = b.lines[len(b.lines)-b.cap:]
		}
	}
}

func (b *logBuffer) last(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > b.cap {
		n = b.cap
	}
	if len(b.lines) <= n {
		out := make([]string, len(b.lines))
		copy(out, b.lines)
		return out
	}
	out := make([]string, n)
	copy(out, b.lines[len(b.lines)-n:])
	return out
}

// teeWriter writes to an underlying writer and to the global log buffer.
type teeWriter struct {
	w  io.Writer
	lb *logBuffer
}

func (t *teeWriter) Write(p []byte) (int, error) {
	// forward to base writer first
	n, err := t.w.Write(p)
	// also append to buffer line-wise
	// ensure we only append the bytes that were written
	buf := p[:n]
	// Normalize to UTF-8 lines; if binary, it's fine to append as-is
	// Accumulate and split by newlines
	lines := bytes.Split(buf, []byte("\n"))
	for i, ln := range lines {
		// Avoid dropping a trailing empty segment when the chunk ends with \n;
		// append empty only if it's not the last segment.
		if len(ln) == 0 && i == len(lines)-1 {
			continue
		}
		t.lb.append(string(ln))
	}
	return n, err
}

var (
	// globalLogBuffer holds recent log lines for GUI consumption.
	globalLogBuffer = newLogBuffer(200) // keep some history; GUI will show last 30
)

// getLastLogLines returns the last n log lines joined with newlines.
func getLastLogLines(n int) string {
	return strings.Join(globalLogBuffer.last(n), "\n")
}

func SetupLogPanel() {
	// Install a tee writer so standard log output is mirrored to our buffer.
	base := os.Stderr
	// Preserve any existing logger flags and prefix; only swap the output.
	log.SetOutput(&teeWriter{w: base, lb: globalLogBuffer})
}
