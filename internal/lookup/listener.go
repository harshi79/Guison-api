package lookup

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ListenForInvalidations keeps caches coherent across horizontally scaled API
// replicas. PostgreSQL delivers the notification only after the import commits.
func ListenForInvalidations(ctx context.Context, pool *pgxpool.Pool, cache *Service) {
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			retry(ctx, err)
			continue
		}
		if _, err := conn.Exec(ctx, `LISTEN bin_data_changed`); err != nil {
			conn.Release()
			retry(ctx, err)
			continue
		}
		for ctx.Err() == nil {
			notification, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				break
			}
			cache.Purge()
			slog.Info("lookup cache invalidated", "source", notification.Payload)
		}
		conn.Release()
	}
}

func retry(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	slog.Warn("cache invalidation listener disconnected", "error", err)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}
