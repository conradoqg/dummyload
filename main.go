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

var (
	targetCPU   float64
	targetMemMB uint64
	dutyCycle   float64
	actualCPU   float64

	cpuMutex sync.RWMutex

	memBuffer []byte
	memMutex  sync.Mutex
)

func main() {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "Unsupported OS: dummyload only supports Linux")
		os.Exit(1)
	}

	var port int
	flag.Float64Var(&targetCPU, "cpu", 0, "target CPU usage percentage (0-100)")
	flag.Uint64Var(&targetMemMB, "mem", 0, "target memory usage in MB")
	flag.IntVar(&port, "port", 8081, "REST API port")
	flag.Parse()

	if targetCPU < 0 || targetCPU > 100 {
		fmt.Fprintln(os.Stderr, "Invalid cpu value: must be between 0 and 100")
		os.Exit(1)
	}

	dutyCycle = targetCPU / 100.0

	if targetMemMB > 0 {
		allocMem(targetMemMB)
	}

	prev := readStats()

	go cpuController(prev)
	go cpuWorker()

	http.HandleFunc("/api/v1/load", loadHandler)
	// Serve OpenAPI spec and Swagger UI
	http.HandleFunc("/swagger.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(swaggerSpec)
	})
	http.HandleFunc("/docs", docsHandler)
	http.HandleFunc("/docs/", docsHandler)
	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting dummyload: cpu=%.1f%%, mem=%dMB, listening on %s\n", targetCPU, targetMemMB, addr)
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

func cpuController(prev cpuStats) {
	period := 100 * time.Millisecond
	Kp := 0.1

	for {
		time.Sleep(period)

		current := readStats()
		idleDelta := current.idle - prev.idle
		totalDelta := current.total - prev.total
		var usage float64
		if totalDelta > 0 {
			usage = (1.0 - float64(idleDelta)/float64(totalDelta)) * 100.0
		} else {
			usage = 0
		}
		prev = current

		cpuMutex.Lock()
		actualCPU = usage

		target := targetCPU
		error := target - usage
		dutyCycle += (Kp * error) / 100.0
		if dutyCycle < 0 {
			dutyCycle = 0
		} else if dutyCycle > 1 {
			dutyCycle = 1
		}
		cpuMutex.Unlock()
	}
}

func cpuWorker() {
	period := 100 * time.Millisecond

	for {
		cpuMutex.RLock()
		dc := dutyCycle
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
			if c < 0 || c > 100 {
				http.Error(w, "cpu must be between 0 and 100", http.StatusBadRequest)
				return
			}
			cpuMutex.Lock()
			targetCPU = c
			dutyCycle = c / 100.0
			cpuMutex.Unlock()
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
