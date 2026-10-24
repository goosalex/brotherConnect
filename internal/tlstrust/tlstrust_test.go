package tlstrust

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

// The bundled roots are pinned: replacing one must be a deliberate change to
// this list and the package comment.
var wantRoots = map[string]string{
	"ISRG Root X1": "96bcec06264976f37460779acf28c5a7cfe8a3c0aae11a8ffcee05c0bddf08c6",
	"ISRG Root X2": "69729b8e15a86efc177a57afb7171dfc64add28c2fca8cf1507e34453ccb1470",
	"Root YE":      "e14ffcad5b0025731006caa43a121a22d8e9700f4fb9cf852f02a708aa5d5666",
	"Root YR":      "e57b7e6f150c419102e8d5c055729ff967b9d1a829bf00cec89ca604ebf4a86f",
}

func TestBundledRootsArePinnedSelfSignedCAs(t *testing.T) {
	roots, err := BundledRoots()
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != len(wantRoots) {
		t.Fatalf("got %d bundled roots, want %d", len(roots), len(wantRoots))
	}
	for _, c := range roots {
		sum := sha256.Sum256(c.Raw)
		want, ok := wantRoots[c.Subject.CommonName]
		if !ok || hex.EncodeToString(sum[:]) != want {
			t.Errorf("unexpected root %q with SHA-256 %x", c.Subject.CommonName, sum)
		}
		if !c.IsCA || c.Subject.String() != c.Issuer.String() {
			t.Errorf("%s is not a self-signed CA", c.Subject.CommonName)
		}
		if err := c.CheckSignatureFrom(c); err != nil {
			t.Errorf("%s self-signature: %v", c.Subject.CommonName, err)
		}
		if c.NotAfter.Before(time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("%s expires %s", c.Subject.CommonName, c.NotAfter)
		}
	}
}

func readChain(t *testing.T) (leaf *x509.Certificate, intermediates *x509.CertPool) {
	t.Helper()
	intermediates = x509.NewCertPool()
	for i := 0; ; i++ {
		der, err := os.ReadFile("testdata/chain-" + string(rune('0'+i)) + ".der")
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			leaf = c
		} else {
			intermediates.AddCert(c)
		}
	}
	if leaf == nil {
		t.Fatal("no chain fixture")
	}
	return leaf, intermediates
}

// TestBundledRootsVerifyTrencitosChain checks the chain the trencitos dev box
// presented on 2026-10-23 against the bundled roots alone, which is what a
// machine with a stale root store (macOS 10.13) falls back to.
func TestBundledRootsVerifyTrencitosChain(t *testing.T) {
	leaf, inter := readChain(t)
	pool, err := poolWithBundledRoots(nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := x509.VerifyOptions{
		DNSName:       "box.dev.trencitos.dev",
		Intermediates: inter,
		Roots:         pool,
		CurrentTime:   time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
	if _, err := leaf.Verify(opts); err != nil {
		t.Fatalf("chain does not verify with the bundled roots: %v", err)
	}

	// Without the server's cross-signs, the self-signed Gen Y root alone must
	// still anchor the chain (Let's Encrypt will drop the cross-signs).
	onlyYE := x509.NewCertPool()
	roots, _ := BundledRoots()
	for _, r := range roots {
		if r.Subject.CommonName == "Root YE" {
			onlyYE.AddCert(r)
		}
	}
	ye1 := x509.NewCertPool()
	der, err := os.ReadFile("testdata/chain-1.der")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	ye1.AddCert(c)
	opts.Roots, opts.Intermediates = onlyYE, ye1
	if _, err := leaf.Verify(opts); err != nil {
		t.Errorf("chain does not verify via self-signed Root YE: %v", err)
	}

	// Hostname checking stays on.
	opts.DNSName = "evil.example"
	if _, err := leaf.Verify(opts); err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Errorf("wrong hostname must fail, got %v", err)
	}
}
