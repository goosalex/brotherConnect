//go:build !legacy

package tlstrust

import (
	"net/http"
	"time"
)

// HTTPClient returns nil in normal builds: callers then use their default
// client, which trusts the operating system's root store.
func HTTPClient(timeout time.Duration) *http.Client { return nil }
