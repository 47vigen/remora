# remora build targets. Engine releases come from engines/versions.env.

include engines/versions.env

IMAGE ?= remora:dev
FAKES_IMAGE ?= remora-fakes:dev
REMORA_VERSION ?= $(shell git describe --tags --always --dirty)
REMORA_COMMIT ?= $(shell git rev-parse --short HEAD)

.PHONY: lint test image fakes-image check-pins e2e

lint:
	golangci-lint run

test:
	go test -race ./...

image:
	docker buildx build --load -t $(IMAGE) \
		--build-arg BIFROST_VERSION=$(BIFROST_VERSION) \
		--build-arg CLIPROXYAPI_VERSION=$(CLIPROXYAPI_VERSION) \
		--build-arg REMORA_VERSION=$(REMORA_VERSION) \
		--build-arg REMORA_COMMIT=$(REMORA_COMMIT) \
		.

fakes-image:
	docker buildx build --load -f test/fakes/Dockerfile -t $(FAKES_IMAGE) .

check-pins:
	scripts/check-engine-pins.sh

# Runs against already-built images; REMORA_IMAGE and FAKES_IMAGE select them.
REMORA_IMAGE ?= $(IMAGE)
export REMORA_IMAGE FAKES_IMAGE

e2e:
	go test -tags e2e -count=1 -timeout 20m ./test/e2e/...
