MODULES := . examples/shared examples/factory examples/publisher
REPORT_DIR ?= $(CURDIR)/reports

.PHONY: tidy build lint test test-ci fmt clean

tidy:
	go work use $(MODULES)
	@for m in $(MODULES); do echo "→ tidy $$m" && cd $$m && go mod tidy && cd -; done

build:
	@for m in $(MODULES); do echo "→ build $$m" && (cd $$m && go build ./...); done

lint:
	@for m in $(MODULES); do echo "→ lint $$m" && (cd $$m && golangci-lint run ./...); done

test:
	@for m in $(MODULES); do echo "→ test $$m" && (cd $$m && go test ./...); done

fmt:
	@for m in $(MODULES); do echo "→ fmt $$m" && (cd $$m && gofmt -w .); done

test-ci:
	mkdir -p $(REPORT_DIR)
	@for m in $(MODULES); do \
		name=$$(echo $$m | tr './' '__'); \
		echo "→ test-ci $$m" && (cd $$m && gotestsum --junitfile $(REPORT_DIR)/$$name.xml -- ./...); \
	done

clean:
	go clean -testcache

run:
	go run ./examples/$(example)/...
