package lookup

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harshi79/project-17/internal/model"
)

type storeFunc func(context.Context, string) (*model.LookupResult, error)

func (f storeFunc) Lookup(ctx context.Context, iin string) (*model.LookupResult, error) {
	return f(ctx, iin)
}

func TestPurgeDoesNotRecacheInflightStaleResult(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	store := storeFunc(func(context.Context, string) (*model.LookupResult, error) {
		call := calls.Add(1)
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
			return &model.LookupResult{Scheme: "old"}, nil
		}
		return &model.LookupResult{Scheme: "new"}, nil
	})
	service := New(store, time.Minute, 10)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = service.Lookup(context.Background(), "123456")
	}()
	<-firstStarted
	service.Purge()

	current, err := service.Lookup(context.Background(), "123456")
	if err != nil || current == nil || current.Scheme != "new" {
		t.Fatalf("current lookup = %+v, %v", current, err)
	}
	close(releaseFirst)
	<-firstDone

	cached, err := service.Lookup(context.Background(), "123456")
	if err != nil || cached == nil || cached.Scheme != "new" {
		t.Fatalf("stale result was recached: %+v, %v", cached, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("store called %d times, want 2", calls.Load())
	}
}
