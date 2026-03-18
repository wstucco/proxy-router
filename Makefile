.PHONY: build

build:
	go build -o proxy-router ./cmd/proxy

install:
	go build -o proxy-router ./cmd/proxy && ./proxy-router install