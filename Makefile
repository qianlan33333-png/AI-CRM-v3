SHELL := /usr/bin/env bash

.PHONY: fmt fmt-check vet test arch build check run

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal && exit 1)

vet:
	GOWORK=off go vet ./...

test:
	GOWORK=off go test ./...

arch:
	python3 scripts/check-architecture.py

build:
	mkdir -p bin
	GOWORK=off go build -o bin/aicrm ./cmd/aicrm

check: fmt-check vet test arch build

run:
	GOWORK=off go run ./cmd/aicrm
