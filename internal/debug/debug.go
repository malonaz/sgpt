package debug

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/malonaz/core/go/pbutil"
	"github.com/malonaz/core/go/pbutil/pbfieldmask"
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

// Init starts the debug HTTP server on a random port and opens it in the default browser.
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

	address := listener.Addr().String()
	url := "http://" + address

	// Best-effort open in browser.
	exec.Command("xdg-open", url).Start()

	return address, nil
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
func LogProto(label string, message proto.Message, readMaskPaths ...string) {
	if !enabled {
		return
	}
	if len(readMaskPaths) > 0 {
		message = proto.Clone(message)
		readMask := pbfieldmask.FromPaths(readMaskPaths...)
		readMask.Apply(message)
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
<style>
body { background: #1a1a2e; color: #e0e0e0; font-family: monospace; padding: 20px; }
.entry { border-bottom: 1px solid #333; padding: 4px 0; }
.ts { color: #7C3AED; }
pre { white-space: pre-wrap; word-wrap: break-word; margin: 2px 0; }
#controls { margin-bottom: 10px; }
button { background: #7C3AED; color: white; border: none; padding: 6px 14px; cursor: pointer; font-family: monospace; margin-right: 8px; }
button:hover { background: #6D28D9; }
</style>
<script>
let refreshInterval = null;
let paused = false;
function togglePause() {
  paused = !paused;
  document.getElementById('pauseBtn').textContent = paused ? 'Resume' : 'Pause';
  if (paused && refreshInterval) { clearInterval(refreshInterval); refreshInterval = null; }
  if (!paused) { startRefresh(); }
}
function startRefresh() {
  refreshInterval = setInterval(function(){ location.reload(); }, 2000);
}
window.onload = function() { startRefresh(); };
</script>
</head><body>
<div id="controls">
  <button id="pauseBtn" onclick="togglePause()">Pause</button>
  <button onclick="fetch('/clear').then(function(){if(!paused) location.reload();})">Clear</button>
</div>
<h2>SGPT Debug Log (%d entries)</h2>`, len(snapshot))
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
