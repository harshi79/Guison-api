package syncer

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAutomaticLoopStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{interval: time.Hour}
	manager.Run(ctx, false)
	cancel()

	done := make(chan struct{})
	go func() {
		manager.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automatic import loop did not stop after cancellation")
	}
}

func TestUpdateAssignmentsDoesNotModifyPrimaryKey(t *testing.T) {
	assignments := updateAssignments()
	for _, key := range []string{"source_id=", "iin_start=", "iin_end="} {
		if strings.Contains(assignments, key) {
			t.Fatalf("upsert modifies primary-key column %q: %s", key, assignments)
		}
	}
	if !strings.Contains(assignments, "scheme=excluded.scheme") {
		t.Fatalf("upsert does not update record data: %s", assignments)
	}
}
