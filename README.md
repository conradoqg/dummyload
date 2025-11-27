# Dummyload

Dummyload is a simple Go-based CLI and containerized tool to generate controlled CPU and memory load (in cores and MB) on Linux systems or within containers (Docker / Kubernetes). It self-regulates to match a specified number of CPU cores (including fractional, e.g. 1.5 cores) and memory footprint, and provides a REST API with an interactive web Control Panel.

## Features
- Generate CPU load across logical cores: fully load N cores and optionally partially load one more.
- Allocate and hold a specified amount of memory (in MB).
- Self-monitor and report actual CPU cores in use.
- REST API (`/api/v1/load`) to GET current/target load and POST adjustments on-the-fly.
- Health configuration API (`/api/v1/health`) to control readiness/liveness behavior at runtime.
- Kubernetes-style readiness (`/readyz`) and liveness (`/livez`) endpoints with configurable success/failure and artificial delay.
- Embedded web Control Panel at `/controlpanel` for easy testing, including sections for load and Kubernetes probes.
- Structured logging for readiness and liveness probe requests (start/completion, status code, latency, bytes).
- Multi-stage Dockerfile for minimal container image.
- `Makefile` for build, run, and Docker workflows.
- VSCode `launch.json` for local debugging with Delve.

## Requirements
- Linux (requires `/proc/stat`).
- Go 1.21+ to build locally.
- Docker (optional, for containerized usage).

## Quickstart

### Build & Run Locally
```bash
# Build binary
make build

# Run with 1.5 CPU cores and 200 MB RAM on port 8080
./dummyload -cores 1.5 -mem 200 -port 8080
```

### Docker
```bash
# Build image
make docker-build

# Run container (exposes port 8080)
make docker-run
```

## REST API

#### GET /api/v1/load
Returns current targets and actual usage:
```json
{
  "target_cores": 1.50,
  "actual_cores": 1.47,
  "target_memory_mb": 200,
  "actual_memory_mb": 200
}
```

#### POST /api/v1/load
Adjust CPU cores and/or memory (both optional):
```bash
curl -X POST http://localhost:8080/api/v1/load \
     -H 'Content-Type: application/json' \
     -d '{"cores":2.0, "mem":150}'
```

Response echoes the updated state.

#### GET /api/v1/health
Returns current readiness/liveness configuration:
```json
{
  "ready": true,
  "live": true,
  "ready_delay_ms": 0,
  "live_delay_ms": 0
}
```

#### POST /api/v1/health
Update readiness/liveness success and/or delays (all fields optional):
```bash
curl -X POST http://localhost:8080/api/v1/health \
     -H 'Content-Type: application/json' \
     -d '{"ready":false, "ready_delay_ms":5000}'
```

Probe endpoints for Kubernetes:
- `GET /readyz` – readiness probe, returns 200/503 and respects `ready_delay_ms`.
- `GET /livez`  – liveness probe, returns 200/503 and respects `live_delay_ms`.

## Control Panel

Browse to http://localhost:8080/controlpanel to view and interact with the API control panel and API documentation. The root path (`/`) serves a simple hello page suitable as a default landing endpoint.

## Configuration Flags
- `-cores` : target load in CPU cores (float; 0 ≤ cores ≤ NumCPU).
- `-mem`   : target memory load in MB (integer ≥ 0).
- `-port`  : HTTP port (default: 8080).
- `-ready` : initial readiness probe success (boolean, default: true).
- `-live`  : initial liveness probe success (boolean, default: true).
- `-ready-delay-ms` : initial readiness probe delay in milliseconds (integer ≥ 0).
- `-live-delay-ms`  : initial liveness probe delay in milliseconds (integer ≥ 0).

## Development
- `make build`     – build `dummyload` binary.
- `make run`       – run with default/example args.
- `make clean`     – remove binary.
- `make docker-*`  – build/run Docker image.

## Debugging in VSCode

Launch the **Debug dummyload** configuration in `.vscode/launch.json` to run under the Go debugger (Delve).

---
## Kubernetes Manifests

We include Kubernetes manifests for deploying and scaling dummyload in the `k8s` directory. To apply, run:

```bash
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/hpa.yaml
```

The sample deployment configures readiness and liveness probes against `/readyz` and `/livez` with a 10-second timeout and approximately 30-second verification window. Ensure you have a metrics server running in your cluster to enable the Horizontal Pod Autoscaler.

Pull requests and issues are welcome! Feel free to contribute improvements or report bugs.
