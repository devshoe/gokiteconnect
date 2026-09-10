package instruments

import (
	"context"
	"time"
)

// Repository stores and queries a normalized instrument catalog. Implementations
// must replace snapshots atomically: a failed Replace must leave the previous
// catalog and refresh time unchanged.
type Repository interface {
	Replace(ctx context.Context, instruments []Instrument, refreshedAt time.Time) error
	LastRefreshedAt(ctx context.Context) (time.Time, error)
	List(ctx context.Context, exchanges []string) ([]Instrument, error)
	Get(ctx context.Context, id InstrumentID) (Instrument, error)
	GetByToken(ctx context.Context, token int64) (Instrument, error)
	Search(ctx context.Context, query string, limit int) ([]Instrument, error)
	ListFO(ctx context.Context, exchanges []string) ([]Instrument, error)
	ListUnderlyingIDs(ctx context.Context, exchanges []string) ([]InstrumentID, error)
	GetFutures(ctx context.Context, filter FuturesFilter) ([]Instrument, error)
	GetOptions(ctx context.Context, filter OptionsFilter) ([]Instrument, error)
	Close() error
}
