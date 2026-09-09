package transport

import "time"

// Backoff computes reconnect delays with exponential growth and a cap
// (Requirements.md §5: "Use exponential backoff"). Full jitter is applied via an
// injectable source so tests stay deterministic.
type Backoff struct {
	Base   time.Duration // delay after the first failure
	Max    time.Duration // ceiling
	Factor float64       // growth multiplier, e.g. 2.0
	// Jitter returns a fraction in [0,1) used to spread delays; nil disables
	// jitter (delay is used as-is). In production wire this to a rand source.
	Jitter func() float64

	attempt int
}

// DefaultBackoff returns a sensible reconnect backoff (0.5s base, 30s cap).
func DefaultBackoff() *Backoff {
	return &Backoff{Base: 500 * time.Millisecond, Max: 30 * time.Second, Factor: 2.0}
}

// Reset clears the attempt counter after a successful connection.
func (b *Backoff) Reset() { b.attempt = 0 }

// Next returns the delay before the next reconnect attempt and advances the
// counter. The raw exponential delay is capped at Max, then optionally reduced
// by full jitter to a value in [delay/2, delay).
func (b *Backoff) Next() time.Duration {
	delay := float64(b.Base)
	for i := 0; i < b.attempt; i++ {
		delay *= b.Factor
		if delay >= float64(b.Max) {
			delay = float64(b.Max)
			break
		}
	}
	b.attempt++

	d := time.Duration(delay)
	if d > b.Max {
		d = b.Max
	}
	if b.Jitter != nil {
		// Full jitter: keep half the delay fixed, jitter the other half.
		half := d / 2
		d = half + time.Duration(b.Jitter()*float64(half))
	}
	return d
}
