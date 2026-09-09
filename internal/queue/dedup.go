package queue

import (
	"sync"
	"time"
)

// DefaultRetention is how long terminally-processed job IDs are remembered for
// duplicate suppression (Requirements.md §10). Deduplication is scoped per
// printer.
const DefaultRetention = 7 * 24 * time.Hour

// dedupStore remembers job IDs that have been terminally processed, per printer,
// so a redelivered job is not printed twice. Requirements.md §8/§10 and
// acceptance criterion 8.
//
// It is safe for concurrent use.
type dedupStore struct {
	retention time.Duration
	now       func() time.Time

	mu   sync.Mutex
	seen map[string]map[string]time.Time // printerID -> jobID -> processedAt
}

func newDedupStore(retention time.Duration, now func() time.Time) *dedupStore {
	if retention <= 0 {
		retention = DefaultRetention
	}
	if now == nil {
		now = time.Now
	}
	return &dedupStore{
		retention: retention,
		now:       now,
		seen:      map[string]map[string]time.Time{},
	}
}

// seenRecently reports whether (printerID, jobID) was processed within the
// retention window. It prunes expired entries as a side effect.
func (d *dedupStore) seenRecently(printerID, jobID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pruneLocked()
	byJob, ok := d.seen[printerID]
	if !ok {
		return false
	}
	_, ok = byJob[jobID]
	return ok
}

// record marks (printerID, jobID) as terminally processed now.
func (d *dedupStore) record(printerID, jobID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	byJob, ok := d.seen[printerID]
	if !ok {
		byJob = map[string]time.Time{}
		d.seen[printerID] = byJob
	}
	byJob[jobID] = d.now()
}

// pruneLocked drops entries older than the retention window. Caller holds mu.
func (d *dedupStore) pruneLocked() {
	cutoff := d.now().Add(-d.retention)
	for pid, byJob := range d.seen {
		for jid, at := range byJob {
			if at.Before(cutoff) {
				delete(byJob, jid)
			}
		}
		if len(byJob) == 0 {
			delete(d.seen, pid)
		}
	}
}
