package flavorcache

import (
	"fmt"
	"sync"
	"time"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
)

const StaledTime time.Duration = 300 * time.Second
const RefreshFailureTime time.Duration = 60 * time.Second // This controls how often we try to get a look at a failed flavor

type openStackInstanceService interface {
	GetFlavorID(flavorName string) (string, error)
	GetFlavorInfo(flavorID string) (flavor *flavors.Flavor, err error)
}

type flavorEntry struct {
	flavorInfo *flavors.Flavor
	err        error
	updated    time.Time
}

type Cache struct {
	cacheMutex sync.Mutex
	cache      map[string]flavorEntry
}

// needsRefresh is unexported and assumes a read lock has been acquired
func (fc *Cache) needsRefresh(flavorName string, now time.Time) bool {
	entry, ok := fc.cache[flavorName]

	// entry not found
	if !ok {
		return true
	}

	// stale valid entry
	if entry.err == nil && now.Sub(entry.updated) > StaledTime {
		return true
	}

	// stale errored entry
	if entry.err != nil && now.Sub(entry.updated) > RefreshFailureTime {
		return true
	}

	// fresh entry, either valid or errored
	return false
}

// refresh is unexported and assumes a write lock has been acquired
func (fc *Cache) refresh(osService openStackInstanceService, flavorName string) {
	flavorID, err := osService.GetFlavorID(flavorName)
	if err != nil {
		fc.cache[flavorName] = flavorEntry{
			updated: time.Now(),
			err:     fmt.Errorf("failed to resolve flavor ID: %w", err),
		}
		return
	}

	flavorInfo, err := osService.GetFlavorInfo(flavorID)
	if err != nil {
		fc.cache[flavorName] = flavorEntry{
			flavorInfo: flavorInfo,
			updated:    time.Now(),
			err:        fmt.Errorf("failed to find flavor information: %w", err),
		}
		return
	}

	fc.cache[flavorName] = flavorEntry{
		flavorInfo: flavorInfo,
		updated:    time.Now(),
	}
}

func New() *Cache {
	return &Cache{
		cache: make(map[string]flavorEntry),
	}
}

// Get returns flavor information, or an error, as retrieved less than
// ${cache-ttl} ago. The cache TTL is different for successful and unsuccessful
// results; see StaledTime and RefreshFailureTime above.
func (fc *Cache) Get(osService openStackInstanceService, flavorName string) (*flavors.Flavor, error) {
	fc.cacheMutex.Lock()
	defer fc.cacheMutex.Unlock()

	if fc.needsRefresh(flavorName, time.Now()) {
		fc.refresh(osService, flavorName)
	}

	flavorEntry := fc.cache[flavorName]

	return flavorEntry.flavorInfo, flavorEntry.err
}
