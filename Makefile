REPORT_DIR ?= $(CURDIR)/reports

.PHONY: tidy build lint test test-ci fmt clean

tidy:
	go mod tidy

build:
	go build ./...

lint:
	golangci-lint run ./...

test:
	go test ./...

fmt:
	gofmt -w .

test-ci:
	mkdir -p $(REPORT_DIR)
	gotestsum --junitfile $(REPORT_DIR)/gonveyor.xml -- ./...

clean:
	go clean -testcache
