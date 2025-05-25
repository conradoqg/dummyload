APP := dummyload
VERSION := latest

.PHONY: help build run clean docker-build docker-run

help:
	@echo "Usage:"
	@echo "  make build         Build the application binary"
	@echo "  make run           Run the application (after build)"
	@echo "  make clean         Remove generated files"
	@echo "  make docker-build  Build the Docker image"
	@echo "  make docker-run    Build and run the Docker image"

build:
	go build -ldflags="-s -w" -o $(APP) ./cmd/$(APP)

run: build
	./$(APP) -cores 1.5 -mem 200 -port 8080

clean:
	rm -f $(APP)

docker-build:
	docker build -t $(APP):$(VERSION) .

docker-run: docker-build
	docker run --rm -p 8080:8080 $(APP):$(VERSION)