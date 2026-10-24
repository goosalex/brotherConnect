//go:build legacy

package tlstrust

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"
)

// HTTPClient returns a client that trusts the system roots plus the bundled
// Let's Encrypt roots (see the package comment). A zero timeout means none,
// which the WebSocket dialer requires.
func HTTPClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsConfig()
	return &http.Client{Transport: tr, Timeout: timeout}
}

// tlsConfig adds the bundled roots to a copy of the system pool. On macOS, Go
// keeps using the platform verifier for such a pool and consults the added
// roots only when that fails. If the bundle cannot be loaded (a build defect,
// covered by tests) the system defaults are used unchanged.
func tlsConfig() *tls.Config {
	sys, err := x509.SystemCertPool()
	if err != nil {
		sys = nil
	}
	pool, err := poolWithBundledRoots(sys)
	if err != nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
