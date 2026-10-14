# remora build targets. Engine releases come from engines/versions.env.

include engines/versions.env

IMAGE ?= remora:dev
FAKES_IMAGE ?= remora-fakes:dev
REMORA_VERSION ?= $(shell git describe --tags --always --dirty)
REMORA_COMMIT ?= $(shell git rev-parse --short HEAD)

.PHONY: lint test image fakes-image check-pins

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
