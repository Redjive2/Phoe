package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// logPath returns the absolute path to the LSP's debug log. We
// hardcode `/tmp/pho-lsp.log` rather than using os.TempDir() because
// on macOS the latter resolves to a per-user `/var/folders/.../T`
// path that nobody can find by guessing. The log is debug output,
// not durable state — `/tmp` on Unix works fine for both macOS and
// Linux.
func logPath() string {
	return "/tmp/pho-lsp.log"
}

var (
	logMu         sync.Mutex
	logFile       *os.File
	logOpenFailed bool // set once the first OpenFile fails, so we stop retrying
)

// logf writes a single timestamped line to the LSP log. Cheap, append-
// only, never raises errors back to the caller — logging is best-
// effort. Goroutine-safe via logMu.
//
// The log captures things Zed (and most LSP clients) hide from the
// user by default: panics, malformed messages, lint internal errors.
// Without it, debugging a stuck server requires bouncing the editor.
func logf(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()

	if logFile == nil {
		if logOpenFailed {
			return // opening failed once already — don't syscall on every log
		}
		f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			// Fall back to stderr; Zed captures it to its own logs.
			fmt.Fprintf(os.Stderr, "pho-lsp: log open failed: %v\n", err)
			logOpenFailed = true
			return
		}
		logFile = f
	}

	ts := time.Now().Format("2006-01-02T15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(logFile, "[%s] %s\n", ts, msg)
}
