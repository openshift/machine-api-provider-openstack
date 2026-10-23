/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package version

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/blang/semver/v4"
)

var (
	gitCommit    string // sha1 from git, output of $(git rev-parse HEAD)
	gitTreeState string // state of git tree, either "clean" or "dirty"
	buildDate    string // build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
)

type Info struct {
	GitCommit    string `json:"gitCommit,omitempty"`
	GitTreeState string `json:"gitTreeState,omitempty"`
	BuildDate    string `json:"buildDate,omitempty"`
	GoVersion    string `json:"goVersion,omitempty"`
	Compiler     string `json:"compiler,omitempty"`
	Platform     string `json:"platform,omitempty"`
}

func Get() Info {
	return Info{
		GitCommit:    gitCommit,
		GitTreeState: gitTreeState,
		BuildDate:    buildDate,
		GoVersion:    runtime.Version(),
		Compiler:     runtime.Compiler,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// Reusing version config from other providers.
// TODO Set the Raw during build.
var (
	// Raw is the string representation of the version. This will be replaced
	// with the calculated version at build time.
	Raw = "v0.0.0-was-not-built-properly"

	// Version is semver representation of the version.
	Version semver.Version

	// String is the human-friendly representation of the version.
	String string
)

func init() {
	// Try to parse the version string as semver
	v, err := semver.Parse(strings.TrimLeft(Raw, "v"))
	if err != nil {
		// If parsing fails (e.g., Raw is a commit hash like "2a0523c"),
		// fall back to a default version. This ensures the program can
		// still run even if the version wasn't properly set at build time
		// or if the repository has no git tags.
		Version = semver.MustParse("0.0.0")
	} else {
		Version = v
	}
	String = fmt.Sprintf("MachineAPIProviderOpenstack %s", Raw)
}
