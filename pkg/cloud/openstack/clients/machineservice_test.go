/*
Copyright 2018 The Kubernetes Authors.

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

package clients

import (
	"strings"
	"testing"
)

func TestMachineServiceInstance(t *testing.T) {
	_, err := NewInstanceService()
	if !(strings.Contains(err.Error(), "[auth_url]")) {
		t.Errorf("Couldn't create instance service: %v", err)
	}
}

func TestDeduplicateLists(t *testing.T) {
	list1 := []string{"1", "2", "3", "a", "b", "c"}
	list2 := []string{"1", "c"}

	// Case 1: Lists with no duplicates has same elements
	result := deduplicateList(list1)
	if !equal(result, list1) {
		t.Errorf("List with no duplicates should contain the same elements. \n\tExpected:%v\n\tGot:%v", list1, result)
	}

	// Case 2: Lists with duplicates have coppies of elements removed
	dupe1 := append(list1, list2...)
	result = deduplicateList(dupe1)
	if !equal(list1, result) {
		t.Errorf("List with duplicates should have coppies of elements removed.\n\tExpected:%v\n\tGot:%v", list1, result)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, v := range a {
		m[v] = true
	}
	for _, v := range b {
		if _, ok := m[v]; !ok {
			return false
		}
	}
	return true
}
