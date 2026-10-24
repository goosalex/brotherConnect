//go:build legacy

package tlstrust

import (
	"crypto/x509"
	"net/http"
	"testing"
	"time"
)

func TestLegacyClientTrustsBundledRoots(t *testing.T) {
	c := HTTPClient(0)
	if c == nil {
		t.Fatal("legacy build must return a client")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("legacy client must set RootCAs")
	}
	leaf, inter := readChain(t)
	opts := x509.VerifyOptions{
		DNSName:       "box.dev.trencitos.dev",
		Intermediates: inter,
		Roots:         tr.TLSClientConfig.RootCAs,
		CurrentTime:   time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
	if _, err := leaf.Verify(opts); err != nil {
		t.Fatalf("legacy client pool does not verify the trencitos chain: %v", err)
	}
}
