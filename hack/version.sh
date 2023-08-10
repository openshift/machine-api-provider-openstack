#!/usr/bin/env bash
# Copyright 2020 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# 	http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

version::get_version_vars() {
    # shellcheck disable=SC1083
    GIT_COMMIT="$(git rev-parse HEAD^{commit})"

    if git_status=$(git status --porcelain 2>/dev/null) && [[ -z ${git_status} ]]; then
        GIT_TREE_STATE="clean"
    else
        GIT_TREE_STATE="dirty"
    fi
}

# stolen from k8s.io/hack/lib/version.sh and modified
# Prints the value that needs to be passed to the -ldflags parameter of go build
version::ldflags() {
    version::get_version_vars

    local -a ldflags
    function add_ldflag() {
        local key=${1}
        local val=${2}
        ldflags+=(
            "-X 'github.com/openshift/machine-api-provider-openstack/version.${key}=${val}'"
        )
    }

    add_ldflag "buildDate" "$(date ${SOURCE_DATE_EPOCH:+"--date=@${SOURCE_DATE_EPOCH}"} -u +'%Y-%m-%dT%H:%M:%SZ')"
    add_ldflag "gitCommit" "${GIT_COMMIT}"
    add_ldflag "gitTreeState" "${GIT_TREE_STATE}"

  # The -ldflags parameter takes a single string, so join the output.
  echo "${ldflags[*]-}"
}

version::ldflags
