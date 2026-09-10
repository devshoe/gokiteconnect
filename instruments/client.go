package instruments

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	kiteconnect "github.com/devshoe/gokiteconnect"
)

// DefaultSearchLimit is used when Search receives a non-positive limit.
const DefaultSearchLimit = 50

// Client owns the lifecycle of a normalized instrument catalog.
type Client struct {
	kite       *kiteconnect.Client
	repository Repository
	refreshMu  sync.Mutex
	closeOnce  sync.Once
	closeErr   error
	now        func() time.Time
}

// NewClient creates a catalog client and refreshes repository when it has not
// been refreshed during the current calendar day in Asia/Kolkata. Ownership of
// repository transfers to Client when NewClient succeeds.
func NewClient(ctx context.Context, kiteClient *kiteconnect.Client, repository Repository) (*Client, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if kiteClient == nil {
		return nil, fmt.Errorf("%w: Kite client is required", ErrInvalidInput)
	}
	if repository == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidInput)
	}
	client := &Client{
		kite:       kiteClient,
		repository: repository,
		now:        time.Now,
	}
	if _, err := client.RefreshIfStale(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// Close closes the repository. It is safe to call more than once.
func (c *Client) Close() error {
	if c == nil || c.repository == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closeErr = c.repository.Close()
	})
	return c.closeErr
}

// Refresh fetches and atomically replaces the complete instrument catalog.
func (c *Client) Refresh(ctx context.Context) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.refreshLocked(ctx)
}

// RefreshIfStale refreshes the catalog if it has not been refreshed today in
// Asia/Kolkata. The returned boolean reports whether a refresh was performed.
func (c *Client) RefreshIfStale(ctx context.Context) (bool, error) {
	if err := validateContext(ctx); err != nil {
		return false, err
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	lastRefreshed, err := c.repository.LastRefreshedAt(ctx)
	if err != nil {
		return false, err
	}
	if sameIndiaDay(lastRefreshed, c.now()) {
		return false, nil
	}
	if err := c.refreshLocked(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// LastRefreshedAt returns the time of the last successful catalog replacement.
// A catalog that has never been populated returns the zero time.
func (c *Client) LastRefreshedAt(ctx context.Context) (time.Time, error) {
	if err := validateContext(ctx); err != nil {
		return time.Time{}, err
	}
	return c.repository.LastRefreshedAt(ctx)
}

// List returns all instruments, optionally restricted to exchanges.
func (c *Client) List(ctx context.Context, exchanges ...string) ([]Instrument, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	return c.repository.List(ctx, sortedUniqueStrings(exchanges))
}

// Get returns one instrument by its canonical ID.
func (c *Client) Get(ctx context.Context, id InstrumentID) (Instrument, error) {
	if err := validateContext(ctx); err != nil {
		return Instrument{}, err
	}
	normalizedID, err := ParseInstrumentID(string(id))
	if err != nil {
		return Instrument{}, err
	}
	return c.repository.Get(ctx, normalizedID)
}

// GetByToken returns one instrument by its Zerodha instrument token.
func (c *Client) GetByToken(ctx context.Context, token int64) (Instrument, error) {
	if err := validateContext(ctx); err != nil {
		return Instrument{}, err
	}
	if token <= 0 {
		return Instrument{}, fmt.Errorf("%w: instrument token must be positive", ErrInvalidInput)
	}
	return c.repository.GetByToken(ctx, token)
}

// Search returns instruments ranked by exact, prefix, and tokenized matches.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Instrument, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: search query is required", ErrInvalidInput)
	}
	if len(searchTerms(query)) == 0 {
		return nil, fmt.Errorf("%w: search query must contain a searchable term", ErrInvalidInput)
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	return c.repository.Search(ctx, query, limit)
}

// ListFO returns listed underlyings that currently have futures or options.
func (c *Client) ListFO(ctx context.Context, exchanges ...string) ([]Instrument, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	return c.repository.ListFO(ctx, sortedUniqueStrings(exchanges))
}

// ListUnderlyingIDs returns distinct derivative underlying IDs. Exchange
// filters apply to the derivative exchange, such as NFO, BFO, or MCX.
func (c *Client) ListUnderlyingIDs(ctx context.Context, exchanges ...string) ([]InstrumentID, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	return c.repository.ListUnderlyingIDs(ctx, sortedUniqueStrings(exchanges))
}

// GetFutures returns futures matching filter.
func (c *Client) GetFutures(ctx context.Context, filter FuturesFilter) ([]Instrument, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	normalized, err := normalizeFuturesFilter(filter)
	if err != nil {
		return nil, err
	}
	return c.repository.GetFutures(ctx, normalized)
}

// GetOptions returns options matching filter.
func (c *Client) GetOptions(ctx context.Context, filter OptionsFilter) ([]Instrument, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	normalized, err := normalizeOptionsFilter(filter)
	if err != nil {
		return nil, err
	}
	return c.repository.GetOptions(ctx, normalized)
}

// TokenIndex returns a read-only in-memory snapshot of all ID/token mappings.
func (c *Client) TokenIndex(ctx context.Context) (*TokenIndex, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	items, err := c.repository.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return newTokenIndex(items), nil
}

func (c *Client) refreshLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := c.kite.GetInstruments()
	if err != nil {
		return fmt.Errorf("instruments: fetch master list: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	instruments, err := normalizeSnapshot(source)
	if err != nil {
		return err
	}
	if err := c.repository.Replace(ctx, instruments, c.now()); err != nil {
		return err
	}
	return nil
}

func normalizeFuturesFilter(filter FuturesFilter) (FuturesFilter, error) {
	underlyingID, err := ParseInstrumentID(string(filter.UnderlyingID))
	if err != nil {
		return FuturesFilter{}, fmt.Errorf("%w: underlying ID: %v", ErrInvalidInput, err)
	}
	numbers, err := normalizeExpiryNumbers(filter.ExpiryNumbers)
	if err != nil {
		return FuturesFilter{}, err
	}
	return FuturesFilter{
		UnderlyingID:  underlyingID,
		ExpiryNumbers: numbers,
		ExpiryDates:   normalizeExpiryDates(filter.ExpiryDates),
	}, nil
}

func normalizeOptionsFilter(filter OptionsFilter) (OptionsFilter, error) {
	underlyingID, err := ParseInstrumentID(string(filter.UnderlyingID))
	if err != nil {
		return OptionsFilter{}, fmt.Errorf("%w: underlying ID: %v", ErrInvalidInput, err)
	}
	numbers, err := normalizeExpiryNumbers(filter.ExpiryNumbers)
	if err != nil {
		return OptionsFilter{}, err
	}

	var strikes *StrikeRange
	if filter.Strikes != nil {
		strikes = &StrikeRange{Min: filter.Strikes.Min, Max: filter.Strikes.Max}
		if strikes.Min != nil && (math.IsNaN(*strikes.Min) || math.IsInf(*strikes.Min, 0) || *strikes.Min < 0) {
			return OptionsFilter{}, fmt.Errorf("%w: minimum strike must be finite and non-negative", ErrInvalidInput)
		}
		if strikes.Max != nil && (math.IsNaN(*strikes.Max) || math.IsInf(*strikes.Max, 0) || *strikes.Max < 0) {
			return OptionsFilter{}, fmt.Errorf("%w: maximum strike must be finite and non-negative", ErrInvalidInput)
		}
		if strikes.Min != nil && strikes.Max != nil && *strikes.Min > *strikes.Max {
			return OptionsFilter{}, fmt.Errorf("%w: minimum strike exceeds maximum strike", ErrInvalidInput)
		}
	}

	typeSet := make(map[OptionType]struct{}, len(filter.Types))
	for _, optionType := range filter.Types {
		optionType = OptionType(strings.ToUpper(strings.TrimSpace(string(optionType))))
		if optionType != OptionTypeCall && optionType != OptionTypePut {
			return OptionsFilter{}, fmt.Errorf("%w: unsupported option type %q", ErrInvalidInput, optionType)
		}
		typeSet[optionType] = struct{}{}
	}
	types := make([]OptionType, 0, len(typeSet))
	for _, optionType := range []OptionType{OptionTypeCall, OptionTypePut} {
		if _, ok := typeSet[optionType]; ok {
			types = append(types, optionType)
		}
	}

	return OptionsFilter{
		UnderlyingID:  underlyingID,
		ExpiryNumbers: numbers,
		ExpiryDates:   normalizeExpiryDates(filter.ExpiryDates),
		Strikes:       strikes,
		Types:         types,
	}, nil
}

func normalizeExpiryNumbers(numbers []int) ([]int, error) {
	seen := make(map[int]struct{}, len(numbers))
	result := make([]int, 0, len(numbers))
	for _, number := range numbers {
		if number < 0 {
			return nil, fmt.Errorf("%w: expiry numbers cannot be negative", ErrInvalidInput)
		}
		if _, exists := seen[number]; exists {
			continue
		}
		seen[number] = struct{}{}
		result = append(result, number)
	}
	sort.Ints(result)
	return result, nil
}

func normalizeExpiryDates(dates []time.Time) []time.Time {
	seen := make(map[string]struct{}, len(dates))
	result := make([]time.Time, 0, len(dates))
	for _, date := range dates {
		normalized := normalizeDate(date)
		key := dateKey(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	return result
}

func sameIndiaDay(left, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return false
	}
	return dateKey(left) == dateKey(right)
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidInput)
	}
	return ctx.Err()
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
