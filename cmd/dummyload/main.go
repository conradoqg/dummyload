package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed swagger.json
var apiSpec []byte

// Version is the CLI version string
const Version = "0.1.0"

const controlPanelHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>dummyload Control Panel</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css">
  <style>
    body { font-size: 0.8rem; }
    main { margin-top: 1rem; }
    h1 { font-size: 1.3rem; margin-bottom: 0.6rem; }
    h2 { font-size: 1rem; }
    h3 { font-size: 0.9rem; }
    input, button { font-size: 0.8rem; padding: 0.25rem 0.45rem; }
    small { font-size: 0.65rem; }
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
      gap: 1.25rem;
    }
    .status-card {
      padding: 0.7rem;
      border-radius: 0.75rem;
      border: 1px solid rgba(0,0,0,0.05);
      background: rgba(255,255,255,0.9);
    }
    .status-label {
      font-weight: 600;
    }
    .status-value-ok {
      color: #15803d;
      font-weight: 600;
    }
    .status-value-fail {
      color: #b91c1c;
      font-weight: 600;
    }
    .error { color: #b91c1c; }
  </style>
</head>
<body>
  <main class="container">
    <h1>dummyload Control Panel</h1>
    <div class="grid">
      <section class="status-card">
        <h2>Load Configuration</h2>
        <form id="load-form">
          <label for="cpu-input">CPU (cores, e.g. 0.5 or 500m)</label>
          <input type="text" id="cpu-input" name="cpu" placeholder="e.g. 0.5 or 500m" />
          <label for="mem-input">Memory (MiB/GiB, e.g. 256Mi or 1Gi)</label>
          <input type="text" id="mem-input" name="mem" placeholder="e.g. 256Mi or 1Gi" />
          <button type="submit">Set Load</button>
          <small>Leave a field blank to keep its current value.</small>
          <div id="error" class="error"></div>
        </form>
        <hr />
        <h3>Current Load</h3>
        <p><span class="status-label">Target CPU:</span> <span id="target-cpu"></span></p>
        <p><span class="status-label">Actual CPU:</span> <span id="actual-cpu"></span></p>
        <p><span class="status-label">Target Memory:</span> <span id="target-mem"></span></p>
        <p><span class="status-label">Actual Memory:</span> <span id="actual-mem"></span></p>
      </section>
      <section class="status-card">
        <h2>Kubernetes Probes</h2>
        <form id="health-form">
          <h3>Readiness</h3>
          <label><input type="checkbox" id="ready-ok" /> Succeed</label>
          <label for="ready-delay-input">Delay (ms)</label>
          <input type="number" id="ready-delay-input" min="0" value="0" />
          <button type="submit" id="set-ready-btn">Set Readiness</button>
          <h3>Liveness</h3>
          <label><input type="checkbox" id="live-ok" /> Succeed</label>
          <label for="live-delay-input">Delay (ms)</label>
          <input type="number" id="live-delay-input" min="0" value="0" />
          <button type="submit" id="set-live-btn">Set Liveness</button>
          <div id="health-error" class="error"></div>
        </form>
        <hr />
        <h3>Current Probe Status</h3>
        <p><span class="status-label">Readiness:</span> <span id="status-ready"></span></p>
        <p><span class="status-label">Liveness:</span> <span id="status-live"></span></p>
      </section>
    </div>
  </main>
  <script>
    let healthFormInitialized = false;
    function parseCpu(str) {
      const m = str.match(/^([0-9.]+)m$/);
      if (m) {
        return parseFloat(m[1]) / 1000;
      }
      const v = parseFloat(str);
      if (isNaN(v)) throw 'Invalid CPU value';
      return v;
    }
    function parseMem(str) {
      const mMi = str.match(/^([0-9.]+)Mi$/i);
      if (mMi) return parseFloat(mMi[1]);
      const mGi = str.match(/^([0-9.]+)Gi$/i);
      if (mGi) return parseFloat(mGi[1]) * 1024;
      const v = parseFloat(str);
      if (isNaN(v)) throw 'Invalid memory value';
      return v;
    }
    function toCpuStr(v) {
      if (v < 1) {
        return (v * 1000).toFixed(0) + 'm';
      }
      return v.toFixed(2).replace(/\.00$/, '');
    }
    function toMemStr(v) {
      if (v % 1024 === 0) {
        return (v / 1024) + 'Gi';
      }
      return v + 'Mi';
    }
    async function fetchStatus() {
      try {
        const res = await fetch('/api/v1/load');
        const data = await res.json();
        document.getElementById('target-cpu').textContent = toCpuStr(data.target_cores);
        document.getElementById('actual-cpu').textContent = data.actual_cores.toFixed(2);
        document.getElementById('target-mem').textContent = toMemStr(data.target_memory_mb);
        document.getElementById('actual-mem').textContent = data.actual_memory_mb + 'Mi';
      } catch(err) {
        console.error('Fetch status error', err);
      }
    }
    async function fetchHealth() {
      try {
        const res = await fetch('/api/v1/health');
        const data = await res.json();
        const readySpan = document.getElementById('status-ready');
        const liveSpan = document.getElementById('status-live');
        readySpan.textContent = data.ready ? 'OK' : 'FAIL';
        liveSpan.textContent = data.live ? 'OK' : 'FAIL';
        readySpan.className = data.ready ? 'status-value-ok' : 'status-value-fail';
        liveSpan.className = data.live ? 'status-value-ok' : 'status-value-fail';
        if (!healthFormInitialized) {
          document.getElementById('ready-ok').checked = data.ready;
          document.getElementById('live-ok').checked = data.live;
          document.getElementById('ready-delay-input').value = data.ready_delay_ms;
          document.getElementById('live-delay-input').value = data.live_delay_ms;
          healthFormInitialized = true;
        }
      } catch(err) {
        console.error('Fetch health error', err);
      }
    }
    document.getElementById('load-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      const errDiv = document.getElementById('error');
      errDiv.textContent = '';
      try {
        const cpuVal = document.getElementById('cpu-input').value;
        const memVal = document.getElementById('mem-input').value;
        const payload = {};
        if (cpuVal) payload.cores = parseCpu(cpuVal);
        if (memVal) payload.mem = Math.round(parseMem(memVal));
        await fetch('/api/v1/load', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        document.getElementById('cpu-input').value = '';
        document.getElementById('mem-input').value = '';
        fetchStatus();
      } catch(err) {
        errDiv.textContent = err;
      }
    });
    document.getElementById('health-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      const errDiv = document.getElementById('health-error');
      errDiv.textContent = '';
      try {
        const submitter = e.submitter ? e.submitter.id : null;
        const payload = {};
        if (!submitter || submitter === 'set-ready-btn') {
          const ready = document.getElementById('ready-ok').checked;
          const readyDelay = parseInt(document.getElementById('ready-delay-input').value || '0', 10);
          payload.ready = ready;
          payload.ready_delay_ms = readyDelay;
        }
        if (!submitter || submitter === 'set-live-btn') {
          const live = document.getElementById('live-ok').checked;
          const liveDelay = parseInt(document.getElementById('live-delay-input').value || '0', 10);
          payload.live = live;
          payload.live_delay_ms = liveDelay;
        }
        await fetch('/api/v1/health', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        fetchHealth();
      } catch(err) {
        errDiv.textContent = err;
      }
    });
    fetchStatus();
    fetchHealth();
    setInterval(() => {
      fetchStatus();
      fetchHealth();
    }, 2000);
  </script>
</body>
</html>`

const homeHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>dummyload</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css">
</head>
<body>
  <main class="container">
    <h1>dummyload</h1>
    <p>Hello from <code>dummyload</code>. This service exposes a REST API for generating CPU and memory load, plus Kubernetes-style readiness and liveness probes.</p>
    <p>Use the <a href="/controlpanel">Control Panel</a> to interactively configure load and probe behavior.</p>
  </main>
</body>
</html>`

const MB = 1024 * 1024

type cpuStats struct {
	idle, total uint64
}

// controlPanelHandler serves the Control Panel HTML
func controlPanelHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(controlPanelHTML))
}

// homeHandler serves a simple hello page at /
func homeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(homeHTML))
}

// Globals for CPU and memory load
var (
	targetCPU   float64   // desired CPU load in cores (fractional)
	targetMemMB uint64    // desired memory load in MB
	workerDuty  []float64 // per-worker duty cycle (0..1)
	actualCPU   float64   // measured CPU load in cores

	cpuMutex sync.RWMutex

	memBuffer []byte
	memMutex  sync.Mutex
	numCPU    int

	healthMutex   sync.RWMutex
	readyOK       = true
	liveOK        = true
	readyDelay    time.Duration
	liveDelay     time.Duration
)

func main() {
	var port int
	var versionFlag bool
	var readyFlag bool
	var liveFlag bool
	var readyDelayMs int
	var liveDelayMs int
	flag.Float64Var(&targetCPU, "cores", 0, "target CPU load in cores (fractional, e.g. 0.5 for half-core)")
	flag.Uint64Var(&targetMemMB, "mem", 0, "target memory usage in MB")
	flag.IntVar(&port, "port", 8081, "REST API port")
	flag.BoolVar(&readyFlag, "ready", true, "initial readiness probe success (true/false)")
	flag.BoolVar(&liveFlag, "live", true, "initial liveness probe success (true/false)")
	flag.IntVar(&readyDelayMs, "ready-delay-ms", 0, "initial readiness probe delay in milliseconds")
	flag.IntVar(&liveDelayMs, "live-delay-ms", 0, "initial liveness probe delay in milliseconds")
	flag.BoolVar(&versionFlag, "version", false, "print version and exit")
	flag.Parse()

	if versionFlag {
		fmt.Printf("dummyload version %s\n", Version)
		os.Exit(0)
	}

	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "Unsupported OS: dummyload only supports Linux")
		os.Exit(1)
	}

	if targetCPU < 0 || targetCPU > float64(runtime.NumCPU()) {
		fmt.Fprintf(os.Stderr, "Invalid cores value: must be between 0 and %d cores\n", runtime.NumCPU())
		os.Exit(1)
	}

	if readyDelayMs < 0 || liveDelayMs < 0 {
		fmt.Fprintln(os.Stderr, "Invalid delay: ready-delay-ms and live-delay-ms must be >= 0")
		os.Exit(1)
	}

	readyOK = readyFlag
	liveOK = liveFlag
	readyDelay = time.Duration(readyDelayMs) * time.Millisecond
	liveDelay = time.Duration(liveDelayMs) * time.Millisecond

	// determine number of CPU cores and setup per-worker duty
	numCPU = runtime.NumCPU()
	workerDuty = make([]float64, numCPU)
	updateWorkerDuty()
	if targetMemMB > 0 {
		allocMem(targetMemMB)
	}

	// start CPU monitor and spawn workers
	go monitorCPU()
	for i := 0; i < numCPU; i++ {
		go cpuWorker(i)
	}

	http.HandleFunc("/api/v1/load", loadHandler)
	http.HandleFunc("/api/v1/health", healthConfigHandler)
	http.Handle("/readyz", loggingMiddleware(http.HandlerFunc(readinessHandler)))
	http.Handle("/livez", loggingMiddleware(http.HandlerFunc(livenessHandler)))
	// Serve OpenAPI spec and Control Panel
	http.HandleFunc("/swagger.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(apiSpec)
	})
	// Serve Control Panel under /controlpanel and a simple hello page at root
	http.HandleFunc("/controlpanel", controlPanelHandler)
	http.HandleFunc("/", homeHandler)
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting dummyload version %s: cores=%.2f, mem=%dMB, workers=%d, ready=%t, live=%t, ready_delay_ms=%d, live_delay_ms=%d, listening on %s",
		Version,
		targetCPU,
		targetMemMB,
		numCPU,
		readyOK,
		liveOK,
		int(readyDelay/time.Millisecond),
		int(liveDelay/time.Millisecond),
		addr,
	)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}

func readStats() cpuStats {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuStats{}
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 1 {
		return cpuStats{}
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 {
		return cpuStats{}
	}
	var idle, total uint64
	var vals []uint64
	for _, s := range fields[1:] {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			v = 0
		}
		vals = append(vals, v)
		total += v
	}
	if len(vals) >= 4 {
		idle = vals[3]
	}
	return cpuStats{idle: idle, total: total}
}

// cpuWorker spins in a loop, busy/sleep according to its duty fraction
func cpuWorker(id int) {
	period := 100 * time.Millisecond
	for {
		cpuMutex.RLock()
		dc := workerDuty[id]
		cpuMutex.RUnlock()
		if dc <= 0 {
			time.Sleep(period)
		} else if dc >= 1 {
			busy(period)
		} else {
			on := time.Duration(dc * float64(period))
			off := period - on
			busy(on)
			time.Sleep(off)
		}
	}
}

func busy(d time.Duration) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
	}
}

// updateWorkerDuty computes per-worker duty fraction from targetCPU
func updateWorkerDuty() {
	cpuMutex.Lock()
	defer cpuMutex.Unlock()
	full := int(targetCPU)
	frac := targetCPU - float64(full)
	for i := 0; i < numCPU; i++ {
		switch {
		case i < full:
			workerDuty[i] = 1.0
		case i == full:
			workerDuty[i] = frac
		default:
			workerDuty[i] = 0.0
		}
	}
}

// monitorCPU periodically samples cgroup or host CPU stats to update actualCPU
func monitorCPU() {
	period := 500 * time.Millisecond
	// try cgroup v1 cpuacct usage (nanoseconds)
	v1Path := "/sys/fs/cgroup/cpuacct/cpuacct.usage"
	if data, err := os.ReadFile(v1Path); err == nil {
		prevUsage, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		for {
			time.Sleep(period)
			data, err := os.ReadFile(v1Path)
			if err != nil {
				continue
			}
			curUsage, err2 := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
			if err2 != nil {
				continue
			}
			delta := curUsage - prevUsage
			prevUsage = curUsage
			cores := float64(delta) / float64(period.Nanoseconds())
			cpuMutex.Lock()
			actualCPU = cores
			cpuMutex.Unlock()
		}
	}
	// try cgroup v2 cpu.stat (usage_usec in microseconds)
	v2Path := "/sys/fs/cgroup/cpu.stat"
	if data, err := os.ReadFile(v2Path); err == nil {
		var prevUsec uint64
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "usage_usec" {
				prevUsec, _ = strconv.ParseUint(fields[1], 10, 64)
				break
			}
		}
		for {
			time.Sleep(period)
			data, err := os.ReadFile(v2Path)
			if err != nil {
				continue
			}
			var curUsec uint64
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == "usage_usec" {
					curUsec, _ = strconv.ParseUint(fields[1], 10, 64)
					break
				}
			}
			deltaUsec := curUsec - prevUsec
			prevUsec = curUsec
			nanodelta := deltaUsec * 1000
			cores := float64(nanodelta) / float64(period.Nanoseconds())
			cpuMutex.Lock()
			actualCPU = cores
			cpuMutex.Unlock()
		}
	}
	// fallback to host /proc/stat
	prev := readStats()
	for {
		time.Sleep(period)
		current := readStats()
		idleDelta := current.idle - prev.idle
		totalDelta := current.total - prev.total
		var usageFrac float64
		if totalDelta > 0 {
			usageFrac = 1.0 - float64(idleDelta)/float64(totalDelta)
		}
		prev = current
		cpuMutex.Lock()
		actualCPU = usageFrac * float64(numCPU)
		cpuMutex.Unlock()
	}
}

func allocMem(mb uint64) {
	size := mb * MB
	memMutex.Lock()
	defer memMutex.Unlock()
	if uint64(len(memBuffer)) < size {
		memBuffer = append(memBuffer, make([]byte, size-uint64(len(memBuffer)))...)
	} else if uint64(len(memBuffer)) > size {
		memBuffer = memBuffer[:size]
	}
	runtime.GC()
	debug.FreeOSMemory()
}

// loadRequest is the JSON body for adjusting load
type loadRequest struct {
	Cores *float64 `json:"cores,omitempty"`
	Mem   *uint64  `json:"mem,omitempty"`
}

// loadResponse is the JSON response for current load
type loadResponse struct {
	TargetCores float64 `json:"target_cores"`
	ActualCores float64 `json:"actual_cores"`
	TargetMemMB uint64  `json:"target_memory_mb"`
	ActualMemMB uint64  `json:"actual_memory_mb"`
}

type healthConfig struct {
	Ready       bool  `json:"ready"`
	Live        bool  `json:"live"`
	ReadyDelay  int64 `json:"ready_delay_ms"`
	LiveDelay   int64 `json:"live_delay_ms"`
}

func loadHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cpuMutex.RLock()
		tCores := targetCPU
		aCores := actualCPU
		cpuMutex.RUnlock()
		memMutex.Lock()
		tMem := targetMemMB
		aMem := uint64(len(memBuffer)) / MB
		memMutex.Unlock()
		resp := loadResponse{
			TargetCores: tCores,
			ActualCores: aCores,
			TargetMemMB: tMem,
			ActualMemMB: aMem,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	case http.MethodPost:
		var req loadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Cores != nil {
			c := *req.Cores
			if c < 0 || c > float64(numCPU) {
				http.Error(w, fmt.Sprintf("cores must be between 0 and %.2f", float64(numCPU)), http.StatusBadRequest)
				return
			}
			cpuMutex.Lock()
			targetCPU = c
			cpuMutex.Unlock()
			// update per-worker duty cycles based on new target cores
			updateWorkerDuty()
		}
		if req.Mem != nil {
			m := *req.Mem
			targetMemMB = m
			allocMem(m)
		}
		w.Header().Set("Content-Type", "application/json")
		cpuMutex.RLock()
		tCores := targetCPU
		aCores := actualCPU
		cpuMutex.RUnlock()
		memMutex.Lock()
		tMem := targetMemMB
		aMem := uint64(len(memBuffer)) / MB
		memMutex.Unlock()
		resp := loadResponse{
			TargetCores: tCores,
			ActualCores: aCores,
			TargetMemMB: tMem,
			ActualMemMB: aMem,
		}
		log.Printf("Configuration updated: target_cores=%.2f, target_memory_mb=%dMB", resp.TargetCores, resp.TargetMemMB)
		json.NewEncoder(w).Encode(resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func readinessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	healthMutex.RLock()
	delay := readyDelay
	ok := readyOK
	healthMutex.RUnlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	if ok {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready\n"))
		return
	}
	http.Error(w, "not ready", http.StatusServiceUnavailable)
}

func livenessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	healthMutex.RLock()
	delay := liveDelay
	ok := liveOK
	healthMutex.RUnlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	if ok {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("live\n"))
		return
	}
	http.Error(w, "not live", http.StatusServiceUnavailable)
}

func currentHealthConfig() healthConfig {
	healthMutex.RLock()
	defer healthMutex.RUnlock()
	return healthConfig{
		Ready:      readyOK,
		Live:       liveOK,
		ReadyDelay: int64(readyDelay / time.Millisecond),
		LiveDelay:  int64(liveDelay / time.Millisecond),
	}
}

func healthConfigHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(currentHealthConfig())
	case http.MethodPost:
		var req struct {
			Ready      *bool  `json:"ready,omitempty"`
			Live       *bool  `json:"live,omitempty"`
			ReadyDelay *int64 `json:"ready_delay_ms,omitempty"`
			LiveDelay  *int64 `json:"live_delay_ms,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		healthMutex.Lock()
		if req.Ready != nil {
			readyOK = *req.Ready
		}
		if req.Live != nil {
			liveOK = *req.Live
		}
		if req.ReadyDelay != nil && *req.ReadyDelay >= 0 {
			readyDelay = time.Duration(*req.ReadyDelay) * time.Millisecond
		}
		if req.LiveDelay != nil && *req.LiveDelay >= 0 {
			liveDelay = time.Duration(*req.LiveDelay) * time.Millisecond
		}
		healthMutex.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(currentHealthConfig())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if lrw.status == 0 {
		lrw.status = http.StatusOK
	}
	n, err := lrw.ResponseWriter.Write(b)
	lrw.size += n
	return n, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w}
		log.Printf("Started %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(lrw, r)
		duration := time.Since(start)
		log.Printf("Completed %s %s with %d in %v (%d bytes)", r.Method, r.URL.Path, lrw.status, duration, lrw.size)
	})
}
