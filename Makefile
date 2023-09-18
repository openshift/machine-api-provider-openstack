SHELL := bash

TESTARGS_DEFAULT := "-v"
export TESTARGS ?= $(TESTARGS_DEFAULT)

HAS_LINT := $(shell command -v golint;)

GOOS ?= $(shell go env GOOS)
LDFLAGS := $(shell source ./hack/version.sh; version::ldflags)

# CTI targets

build: manager

manager:
	CGO_ENABLED=0 GOOS=$(GOOS) go build \
		-ldflags "${LDFLAGS}" \
		-o machine-controller-manager \
		cmd/manager/main.go


test: ## Run tests
	@echo -e "\033[32mTesting...\033[0m"
	hack/ci-test.sh


check: fmt vet lint

unit:
	go test -tags=unit $(shell go list ./...) $(TESTARGS)


.PHONY: check-vendor
check-vendor:
	hack/verify-vendor.sh

fmt:
	hack/verify-gofmt.sh

lint:
ifndef HAS_LINT
		go get -u golang.org/x/lint/golint
		echo "installing golint"
endif
	hack/verify-golint.sh

vet:
	go vet ./...

cover:
	go test -tags=unit $(shell go list ./...) -cover

clean:
	rm -rf _dist bin/manager

realclean: clean
	rm -rf vendor
	if [ "$(GOPATH)" = "$(GOPATH_DEFAULT)" ]; then \
		rm -rf $(GOPATH); \
	fi

version:
	@echo ${VERSION}

.PHONY: build clean cover docs fmt lint realclean \
	test translation version unit
