package printer

import "context"

// emit sends an event without blocking on a slow consumer or a cancelled ctx.
func emit(ctx context.Context, events chan<- Event, ev Event) {
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}
