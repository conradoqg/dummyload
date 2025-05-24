package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
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

const swaggerUIHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>dummyload API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: '/swagger.json',
      dom_id: '#swagger-ui'
    });
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
    targetCPU   float64   // desired load in cores (can be fractional)
    targetMemMB uint64    // desired memory load in MB
    workerDuty  []float64 // per-worker duty cycle (0..1)
    actualCPU   float64   // measured load in cores

    cpuMutex sync.RWMutex

    memBuffer []byte
    memMutex  sync.Mutex
    numCPU    int
)

func main() {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "Unsupported OS: dummyload only supports Linux")
		os.Exit(1)
	}

    var port int
    flag.Float64Var(&targetCPU, "cpu", 0, "target CPU load in cores (fractional, e.g. 0.5 for half-core)")
    flag.Uint64Var(&targetMemMB, "mem", 0, "target memory usage in MB")
    flag.IntVar(&port, "port", 8080, "REST API port")
    flag.Parse()

    // validate cpu value against available cores
    if targetCPU < 0 || targetCPU > float64(runtime.NumCPU()) {
        fmt.Fprintf(os.Stderr, "Invalid cpu value: must be between 0 and %d cores\n", runtime.NumCPU())
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
    fmt.Printf("Starting dummyload: cores=%.2f, mem=%dMB, workers=%d, listening on %s\n", targetCPU, targetMemMB, numCPU, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, "HTTP server error:", err)
		os.Exit(1)
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

// monitorCPU periodically samples /proc/stat to update actualCPU
func monitorCPU() {
    period := 500 * time.Millisecond
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

type loadRequest struct {
	CPU *float64 `json:"cpu,omitempty"`
	Mem *uint64  `json:"mem,omitempty"`
}

type loadResponse struct {
	TargetCPU   float64 `json:"target_cpu"`
	ActualCPU   float64 `json:"actual_cpu"`
	TargetMemMB uint64  `json:"target_memory_mb"`
	ActualMemMB uint64  `json:"actual_memory_mb"`
}

func loadHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cpuMutex.RLock()
		tCPU := targetCPU
		aCPU := actualCPU
		cpuMutex.RUnlock()
		memMutex.Lock()
		tMem := targetMemMB
		aMem := uint64(len(memBuffer)) / MB
		memMutex.Unlock()
		resp := loadResponse{
			TargetCPU:   tCPU,
			ActualCPU:   aCPU,
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
        if req.CPU != nil {
        c := *req.CPU
        if c < 0 || c > float64(numCPU) {
            http.Error(w, fmt.Sprintf("cpu must be between 0 and %.2f cores", float64(numCPU)), http.StatusBadRequest)
            return
        }
        cpuMutex.Lock()
        targetCPU = c
        cpuMutex.Unlock()
        // update per-worker duty cycles based on new target
        updateWorkerDuty()
        }
		if req.Mem != nil {
			m := *req.Mem
			targetMemMB = m
			allocMem(m)
		}
		w.Header().Set("Content-Type", "application/json")
		cpuMutex.RLock()
		tCPU := targetCPU
		aCPU := actualCPU
		cpuMutex.RUnlock()
		memMutex.Lock()
		tMem := targetMemMB
		aMem := uint64(len(memBuffer)) / MB
		memMutex.Unlock()
		resp := loadResponse{
			TargetCPU:   tCPU,
			ActualCPU:   aCPU,
			TargetMemMB: tMem,
			ActualMemMB: aMem,
		}
		json.NewEncoder(w).Encode(resp)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
