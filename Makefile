TESTARGS_DEFAULT := "-v"
export TESTARGS ?= $(TESTARGS_DEFAULT)

HAS_LINT := $(shell command -v golint;)

GOOS ?= $(shell go env GOOS)
VERSION ?= $(shell git describe --exact-match 2> /dev/null || \
                 git describe --match=$(git rev-parse --short=8 HEAD) --always --dirty --abbrev=8)
GOFLAGS   :=
TAGS      :=
LDFLAGS   := "-w -s -X 'main.version=${VERSION}'"
REGISTRY ?= k8scloudprovider

# CTI targets

build: manager

manager:
	CGO_ENABLED=0 GOOS=$(GOOS) go build \
		-ldflags $(LDFLAGS) \
		-o bin/manager \
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

docs:
	@echo "$@ not yet implemented"

godoc:
	@echo "$@ not yet implemented"

releasenotes:
	@echo "Reno not yet implemented for this repo"

translation:
	@echo "$@ not yet implemented"

clean:
	rm -rf _dist bin/manager

realclean: clean
	rm -rf vendor
	if [ "$(GOPATH)" = "$(GOPATH_DEFAULT)" ]; then \
		rm -rf $(GOPATH); \
	fi

images: openstack-cluster-api-controller

openstack-cluster-api-controller: manager
ifeq ($(GOOS),linux)
	cp bin/manager cmd/manager
	docker build -t $(REGISTRY)/openstack-cluster-api-controller:$(VERSION) cmd/manager
	rm cmd/manager/manager
else
	$(error Please set GOOS=linux for building the image)
endif

upload-images: images
	@echo "push images to $(REGISTRY)"
	docker login -u="$(DOCKER_USERNAME)" -p="$(DOCKER_PASSWORD)";
	docker push $(REGISTRY)/openstack-cluster-api-controller:$(VERSION)

version:
	@echo ${VERSION}

.PHONY: build clean cover docs fmt lint realclean \
	test translation version unit
