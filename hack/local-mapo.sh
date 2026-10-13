#!/bin/sh

set -e

go build -o machine-controller ./cmd/manager/main.go

# Remove machine-api-operator from CVO control
./hack/cvo-unmanage.py openshift-machine-api machine-api-operator

# Scale the operator down to zero so it doesn't 'fix' machine-api-controllers
oc -n openshift-machine-api scale deploy/machine-api-operator --replicas 0

# Ensure machine-api-controllers deployment is annotated with
# last-applied-configuration
oc -n openshift-machine-api get deploy/machine-api-controllers -o json | \
    oc apply -f -

# Remove machine-controller from deployment
oc -n openshift-machine-api get deploy/machine-api-controllers -o json | \
    jq 'del(.spec.template.spec.containers[] | select(.name == "machine-controller"))' | \
    oc apply -f -

# Run MAPO locally
./machine-controller --logtostderr=true --v=3 --namespace=openshift-machine-api
