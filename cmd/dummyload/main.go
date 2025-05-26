package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
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
var swaggerSpec []byte

// Version is the CLI version string
const Version = "0.1.0"

const swaggerUIHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>dummyload Control Panel</title>
  <style>
    body { font-family: Arial, sans-serif; margin: 20px; }
    label { display: block; margin-top: 10px; }
    input { margin-top: 5px; padding: 5px; width: 200px; }
    button { margin-top: 10px; padding: 5px 10px; }
    .status { margin-top: 20px; }
    .status div { margin-bottom: 5px; }
    .error { color: red; }
  </style>
</head>
<body>
  <h1>dummyload Control Panel</h1>
  <form id="load-form">
    <label for="cpu-input">CPU (cores, e.g. 0.5 or 500m)</label>
    <input type="text" id="cpu-input" name="cpu" placeholder="e.g. 0.5 or 500m" />
    <label for="mem-input">Memory (MiB/GiB, e.g. 256Mi or 1Gi)</label>
    <input type="text" id="mem-input" name="mem" placeholder="e.g. 256Mi or 1Gi" />
    <button type="submit">Set</button>
    <div id="error" class="error"></div>
  </form>
  <div class="status">
    <h2>Current Configuration</h2>
    <div>Target CPU: <span id="target-cpu"></span></div>
    <div>Actual CPU: <span id="actual-cpu"></span></div>
    <div>Target Memory: <span id="target-mem"></span></div>
    <div>Actual Memory: <span id="actual-mem"></span></div>
  </div>
  <script>
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
    fetchStatus();
    setInterval(fetchStatus, 2000);
  </script>
</body>
</html>`

const MB = 1024 * 1024

type cpuStats struct {
	idle, total uint64
}

// docsHandler serves the Swagger UI HTML
func docsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(swaggerUIHTML))
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
)

func main() {
	var port int
	var versionFlag bool
	flag.Float64Var(&targetCPU, "cores", 0, "target CPU load in cores (fractional, e.g. 0.5 for half-core)")
	flag.Uint64Var(&targetMemMB, "mem", 0, "target memory usage in MB")
	flag.IntVar(&port, "port", 8081, "REST API port")
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
	// Serve OpenAPI spec and Swagger UI
	http.HandleFunc("/swagger.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(swaggerSpec)
	})
	// Serve Swagger UI at root
	http.HandleFunc("/", docsHandler)
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting dummyload version %s: cores=%.2f, mem=%dMB, workers=%d, listening on %s", Version, targetCPU, targetMemMB, numCPU, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}

func readStats() cpuStats {
	data, err := ioutil.ReadFile("/proc/stat")
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
	if data, err := ioutil.ReadFile(v1Path); err == nil {
		prevUsage, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		for {
			time.Sleep(period)
			data, err := ioutil.ReadFile(v1Path)
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
	if data, err := ioutil.ReadFile(v2Path); err == nil {
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
			data, err := ioutil.ReadFile(v2Path)
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
