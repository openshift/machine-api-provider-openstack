#!/bin/sh

if [ "$IS_CONTAINER" != "" ]; then
  set -euxo pipefail
  go mod tidy
  go mod vendor
  go mod verify
  git diff --exit-code vendor go.mod go.sum
else
  podman run --rm \
    --env IS_CONTAINER=TRUE \
    --volume "${PWD}:/go/src/github.com/openshift/installer:z" \
    --workdir /go/src/github.com/openshift/installer \
    docker.io/openshift/origin-release:golang-1.15 \
    ./hack/verify-vendor.sh "${@}"
fi
