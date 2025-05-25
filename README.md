# Dummyload

Dummyload is a simple Go-based CLI and containerized tool to generate controlled CPU and memory load (in cores and MB) on Linux systems or within containers (Docker / Kubernetes). It self-regulates to match a specified number of CPU cores (including fractional, e.g. 1.5 cores) and memory footprint, and provides a REST API with an interactive Swagger UI.

## Features
- Generate CPU load across logical cores: fully load N cores and optionally partially load one more.
- Allocate and hold a specified amount of memory (in MB).
- Self-monitor and report actual CPU cores in use.
- REST API (`/api/v1/load`) to GET current/target load and POST adjustments on-the-fly.
- Embedded Swagger UI at `/` for easy testing.
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

## Swagger UI

Browse to http://localhost:8080/ to view and interact with the API documentation.

## Configuration Flags
- `-cores` : target load in CPU cores (float; 0 ≤ cores ≤ NumCPU).
- `-mem`   : target memory load in MB (integer ≥ 0).
- `-port`  : HTTP port (default: 8080).

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

Ensure you have a metrics server running in your cluster to enable the Horizontal Pod Autoscaler.

Pull requests and issues are welcome! Feel free to contribute improvements or report bugs.