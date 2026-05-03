package debug

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/protobuf/proto"
)

var (
	mu      sync.Mutex
	entries []entry
	server  *http.Server
	enabled bool
)

type entry struct {
	timestamp time.Time
	message   string
}

// Init starts the debug HTTP server on a random port. Returns the address.
func Init(ctx context.Context) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("starting debug listener: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/clear", handleClear)

	server = &http.Server{Handler: mux}
	enabled = true

	go server.Serve(listener)
	go func() {
		<-ctx.Done()
		server.Close()
	}()

	return listener.Addr().String(), nil
}

// Log records a debug message. No-op if Init has not been called.
func Log(format string, args ...any) {
	if !enabled {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	entries = append(entries, entry{
		timestamp: time.Now(),
		message:   fmt.Sprintf(format, args...),
	})
}

// LogProto logs a pretty-printed proto message. No-op if Init has not been called.
func LogProto(label string, message proto.Message) {
	if !enabled {
		return
	}
	bytes, err := pbutil.JSONMarshalPretty(message)
	if err != nil {
		Log("%s: [marshal error: %v]", label, err)
		return
	}
	Log("%s:\n%s", label, string(bytes))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	mu.Lock()
	snapshot := make([]entry, len(entries))
	copy(snapshot, entries)
	mu.Unlock()

	fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>SGPT Debug</title>
<meta http-equiv="refresh" content="2">
<style>
body { background: #1a1a2e; color: #e0e0e0; font-family: monospace; padding: 20px; }
.entry { border-bottom: 1px solid #333; padding: 4px 0; }
.ts { color: #7C3AED; }
pre { white-space: pre-wrap; word-wrap: break-word; margin: 2px 0; }
</style></head><body><h2>SGPT Debug Log (%d entries)</h2>`, len(snapshot))
	for i := len(snapshot) - 1; i >= 0; i-- {
		e := snapshot[i]
		fmt.Fprintf(w, `<div class="entry"><span class="ts">%s</span><pre>%s</pre></div>`,
			e.timestamp.Format("15:04:05.000"),
			strings.ReplaceAll(e.message, "<", "&lt;"))
	}
	fmt.Fprintf(w, `</body></html>`)
}

func handleClear(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	entries = nil
	mu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
