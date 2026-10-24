//go:build legacy

// Fallbacks for the legacy Go 1.20 macOS build (`-tags legacy`, see
// docs/macos-legacy.md): the min and max built-ins arrived in Go 1.21. Normal
// builds never compile this file and use the built-ins.

package printer

type ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64 | ~string
}

func min[T ordered](x T, ys ...T) T {
	for _, y := range ys {
		if y < x {
			x = y
		}
	}
	return x
}

func max[T ordered](x T, ys ...T) T {
	for _, y := range ys {
		if y > x {
			x = y
		}
	}
	return x
}
