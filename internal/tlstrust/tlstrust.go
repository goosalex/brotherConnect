// Package tlstrust supplies the HTTP client the bridge uses for TLS to
// trencitos (the wss:// connection and device enrollment).
//
// Normal builds use Go's defaults, which trust the operating system's root
// store. The legacy macOS 10.13 build (`-tags legacy`, docs/macos-legacy.md)
// cannot rely on that store: High Sierra stopped receiving root updates years
// ago and does not trust ISRG Root X2 or Let's Encrypt's newer roots, which
// the trencitos certificates chain to. That build adds the roots in roots/ to
// the system pool. Go then verifies with the macOS trust store first and only
// falls back to these bundled roots when the system cannot build a chain;
// hostname and expiry checks are unchanged.
//
// Bundled roots (DER, from https://letsencrypt.org/certificates/):
//
//	isrg-root-x1.der  ISRG Root X1  SHA-256 96:BC:EC:06:...:BD:DF:08:C6  expires 2035
//	isrg-root-x2.der  ISRG Root X2  SHA-256 69:72:9B:8E:...:3C:CB:14:70  expires 2040
//	root-ye.der       Root YE       SHA-256 E1:4F:FC:AD:...:AA:5D:56:66  expires 2045
//	root-yr.der       Root YR       SHA-256 E5:7B:7E:6F:...:EB:F4:A8:6F  expires 2045
package tlstrust

import (
	"crypto/x509"
	"embed"
	"fmt"
	"sort"
)

//go:embed roots/*.der
var rootFiles embed.FS

// BundledRoots parses the embedded root certificates, sorted by file name.
func BundledRoots() ([]*x509.Certificate, error) {
	entries, err := rootFiles.ReadDir("roots")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	certs := make([]*x509.Certificate, 0, len(names))
	for _, n := range names {
		der, err := rootFiles.ReadFile("roots/" + n)
		if err != nil {
			return nil, err
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse bundled root %s: %w", n, err)
		}
		certs = append(certs, c)
	}
	return certs, nil
}

// poolWithBundledRoots returns base (or an empty pool) plus the bundled roots.
func poolWithBundledRoots(base *x509.CertPool) (*x509.CertPool, error) {
	roots, err := BundledRoots()
	if err != nil {
		return nil, err
	}
	pool := base
	if pool == nil {
		pool = x509.NewCertPool()
	}
	for _, c := range roots {
		pool.AddCert(c)
	}
	return pool, nil
}
