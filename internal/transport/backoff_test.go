package transport

import (
	"testing"
	"time"
)

func TestBackoffExponentialGrowthAndCap(t *testing.T) {
	b := &Backoff{Base: 100 * time.Millisecond, Max: 1 * time.Second, Factor: 2.0}
	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1 * time.Second, // capped
		1 * time.Second, // stays capped
	}
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Errorf("attempt %d: got %v, want %v", i, got, w)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	b := &Backoff{Base: 100 * time.Millisecond, Max: 1 * time.Second, Factor: 2.0}
	b.Next()
	b.Next()
	b.Reset()
	if got := b.Next(); got != 100*time.Millisecond {
		t.Fatalf("after reset got %v, want base 100ms", got)
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	// Jitter fixed at 0 yields the lower bound (half the delay); at ~1 yields
	// just under the full delay.
	b := &Backoff{Base: 1 * time.Second, Max: 10 * time.Second, Factor: 2.0, Jitter: func() float64 { return 0 }}
	if got := b.Next(); got != 500*time.Millisecond {
		t.Fatalf("jitter=0 got %v, want 500ms (half)", got)
	}
	b.Reset()
	b.Jitter = func() float64 { return 0.999 }
	got := b.Next()
	if got <= 500*time.Millisecond || got >= 1*time.Second {
		t.Fatalf("jitter~1 got %v, want in (500ms, 1s)", got)
	}
}
