package flavorcache

import (
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
)

func newFlavorCache(options ...func(*Cache)) *Cache {
	fc := New()
	for _, apply := range options {
		apply(fc)
	}
	return fc
}

func withCacheEntry(flavorName string, entry flavorEntry) func(*Cache) {
	return func(fc *Cache) {
		fc.cache[flavorName] = entry
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Now()
	flavorName := "m1.super.unleaded"

	needsRefresh := true
	doesNotNeedRefresh := false

	for _, tc := range [...]struct {
		name   string
		fc     *Cache
		expect bool
	}{
		{
			"valid fresh",
			newFlavorCache(withCacheEntry(flavorName, flavorEntry{updated: now})),
			doesNotNeedRefresh,
		},
		{
			"errored fresh",
			newFlavorCache(withCacheEntry(flavorName, flavorEntry{err: io.EOF, updated: now})),
			doesNotNeedRefresh,
		},
		{
			"absent",
			newFlavorCache(),
			needsRefresh,
		},
		{
			"valid stale",
			newFlavorCache(withCacheEntry(flavorName, flavorEntry{updated: now.Add(-StaledTime).Add(-1)})),
			needsRefresh,
		},
		{
			"errored stale",
			newFlavorCache(withCacheEntry(flavorName, flavorEntry{err: io.EOF, updated: now.Add(-RefreshFailureTime).Add(-1)})),
			needsRefresh,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if want, have := tc.expect, tc.fc.needsRefresh(flavorName, now); want != have {
				t.Errorf("expected %v, found %v", want, have)
			}
		})
	}
}

type instanceService struct {
	flavorName    string
	flavorID      string
	flavorIDError error

	flavorInfo      *flavors.Flavor
	flavorInfoError error

	wasCalled bool
}

func (s *instanceService) GetFlavorID(flavorName string) (string, error) {
	s.wasCalled = true
	if flavorName == s.flavorName {
		return s.flavorID, s.flavorIDError
	}
	return "", fmt.Errorf("flavor name NOT FOUND")
}
func (s *instanceService) GetFlavorInfo(flavorID string) (flavor *flavors.Flavor, err error) {
	if flavorID == s.flavorID {
		return s.flavorInfo, s.flavorInfoError
	}
	return nil, fmt.Errorf("NOT FOUND")
}

func newInstanceService(options ...func(*instanceService)) *instanceService {
	var s instanceService
	for _, apply := range options {
		apply(&s)
	}
	return &s
}

func withFlavor(name string, flavor *flavors.Flavor) func(*instanceService) {
	return func(s *instanceService) {
		s.flavorName = name
		s.flavorInfo = flavor
	}
}

func TestGet(t *testing.T) {
	type checkFunc func(*flavors.Flavor, error, *instanceService) error
	that := func(fns ...checkFunc) []checkFunc { return fns }

	serviceWasCalled := func(_ *flavors.Flavor, _ error, s *instanceService) error {
		if !s.wasCalled {
			return fmt.Errorf("instanceService was not called")
		}
		return nil
	}

	serviceWasNotCalled := func(_ *flavors.Flavor, _ error, s *instanceService) error {
		if s.wasCalled {
			return fmt.Errorf("instanceService was called")
		}
		return nil
	}

	returnsFlavorID := func(id string) checkFunc {
		return func(f *flavors.Flavor, _ error, _ *instanceService) error {
			if f == nil {
				return fmt.Errorf("expected flavor, found nil")
			}

			if f.ID != id {
				return fmt.Errorf("expected flavor with ID %q, found %q", id, f.ID)
			}
			return nil
		}
	}

	returnsError := func(_ *flavors.Flavor, err error, _ *instanceService) error {
		if err == nil {
			return fmt.Errorf("expected error, got nil")
		}
		return nil
	}

	noError := func(_ *flavors.Flavor, err error, _ *instanceService) error {
		if err != nil {
			return fmt.Errorf("unexpected error: %v", err)
		}
		return nil
	}

	newFlavor := func(id string) *flavors.Flavor {
		return &flavors.Flavor{ID: id}
	}

	for _, tc := range [...]struct {
		name       string
		flavorName string
		service    *instanceService
		fc         *Cache
		check      []checkFunc
	}{
		{
			name:       "unknown",
			flavorName: "valid",
			service:    newInstanceService(withFlavor("valid", newFlavor("from-service"))),
			fc:         newFlavorCache(),
			check: that(
				serviceWasCalled,
				returnsFlavorID("from-service"),
				noError,
			),
		},
		{
			name:       "valid fresh",
			flavorName: "valid",
			service:    newInstanceService(withFlavor("valid", newFlavor("from-service"))),
			fc:         newFlavorCache(withCacheEntry("valid", flavorEntry{flavorInfo: newFlavor("from-cache"), updated: time.Now()})),
			check: that(
				serviceWasNotCalled,
				returnsFlavorID("from-cache"),
				noError,
			),
		},
		{
			name:       "valid stale",
			flavorName: "valid",
			service:    newInstanceService(withFlavor("valid", newFlavor("from-service"))),
			fc:         newFlavorCache(withCacheEntry("valid", flavorEntry{flavorInfo: newFlavor("from-cache"), updated: time.Now().Add(-StaledTime).Add(-time.Second)})),
			check: that(
				serviceWasCalled,
				returnsFlavorID("from-service"),
				noError,
			),
		},
		{
			name:       "errored fresh",
			flavorName: "errored",
			service:    newInstanceService(withFlavor("errored", newFlavor("from-service"))),
			fc:         newFlavorCache(withCacheEntry("errored", flavorEntry{err: io.EOF, updated: time.Now()})),
			check: that(
				serviceWasNotCalled,
				returnsError,
			),
		},
		{
			name:       "errored stale",
			flavorName: "errored",
			service:    newInstanceService(withFlavor("errored", newFlavor("from-service"))),
			fc:         newFlavorCache(withCacheEntry("errored", flavorEntry{err: io.EOF, updated: time.Now().Add(-RefreshFailureTime).Add(-time.Second)})),
			check: that(
				serviceWasCalled,
				returnsFlavorID("from-service"),
				noError,
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := tc.fc.Get(tc.service, tc.flavorName)
			for _, check := range tc.check {
				if e := check(f, err, tc.service); e != nil {
					t.Error(e)
				}
			}
		})

	}
}
