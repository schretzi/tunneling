#!/bin/bash

BINARY_NAME=tunneling
MAIN_PACKAGE_PATH=.
PACKAGE=github.com/schretzi/tunneling

VERSION=$(shell git describe --tags --always --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2> /dev/null)
COMMIT_HASH=$(shell git rev-parse --short HEAD)
BUILD_TIMESTAMP=$(shell date '+%Y-%m-%dT%H:%M:%S')
BUILD_PATH=$(MAIN_PACKAGE_PATH)/builds

LDFLAGS=-ldflags "-X ${PACKAGE}/tunneling.Version=${VERSION} -X ${PACKAGE}/tunneling.CommitHash=${COMMIT_HASH} -X ${PACKAGE}/tunneling.BuildTimestamp=${BUILD_TIMESTAMP}"

ACTUAL_OS=$(shell uname -o |  tr '[:upper:]' '[:lower:]' )
ACTUAL_ARCH=$(shell uname -m)


# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' |  sed -e 's/^/ /'

.PHONY: confirm
confirm:
	@echo -n 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

.PHONY: no-dirty
no-dirty:
	git diff --exit-code

# ==================================================================================== #
# TEST & BUILD
# ==================================================================================== #

## tidy: format code and tidy modfile
.PHONY: tidy
tidy:
	go fmt ./...
	go mod tidy -v

## audit: run quality control checks
.PHONY: audit
audit:
	go mod verify
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@latest -checks=all,-ST1000,-U1000 ./...
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	go test -race -buildvcs -vet=off ./...


## test: run all tests
.PHONY: test
test:
	go test -v -race -buildvcs ./...

## test/cover: run all tests and display coverage
.PHONY: test/cover
test/cover:
	go test -v -race -buildvcs -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out

## build: build the application
.PHONY: build
build: windows linux darwin
    # Include additional build steps, like TypeScript, SCSS or Tailwind compilation here...
	
windows:
	env GOOS=windows GOARCH=amd64 go build -v -o ${BUILD_PATH}/${BINARY_NAME}_windows_amd64 ${MAIN_PACKAGE_PATH}

linux:
	env GOOS=linux GOARCH=amd64 go build -v -o ${BUILD_PATH}/${BINARY_NAME}_linux_amd64 ${MAIN_PACKAGE_PATH}
	env GOOS=linux GOARCH=arm64 go build -v -o ${BUILD_PATH}/${BINARY_NAME}_linux_arm64 ${MAIN_PACKAGE_PATH}

darwin:
	env GOOS=darwin GOARCH=arm64 go build -v -o ${BUILD_PATH}/${BINARY_NAME}_darwin_arm64 ${MAIN_PACKAGE_PATH}

.PHONY: install
install:
	cp ${BUILD_PATH}/${BINARY_NAME}_${ACTUAL_OS}_${ACTUAL_ARCH} ~/bin/${BINARY_NAME}
