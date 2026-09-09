package queue

import (
	"testing"
	"time"
)

func TestDedupRecordAndSeen(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	clock := now
	d := newDedupStore(time.Hour, func() time.Time { return clock })

	if d.seenRecently("p1", "j1") {
		t.Fatal("unseen job reported as seen")
	}
	d.record("p1", "j1")
	if !d.seenRecently("p1", "j1") {
		t.Fatal("recorded job not reported as seen")
	}
	// Dedup is scoped per printer.
	if d.seenRecently("p2", "j1") {
		t.Fatal("dedup leaked across printers")
	}
}

func TestDedupRetentionExpiry(t *testing.T) {
	clock := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	d := newDedupStore(time.Hour, func() time.Time { return clock })

	d.record("p1", "j1")
	clock = clock.Add(2 * time.Hour) // past retention
	if d.seenRecently("p1", "j1") {
		t.Fatal("expired entry still suppresses; retention not applied")
	}
}
