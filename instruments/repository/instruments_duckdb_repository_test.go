package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kiteconnect "github.com/devshoe/gokiteconnect"
	instrumentcatalog "github.com/devshoe/gokiteconnect/instruments"
)

const catalogCSV = `instrument_token,exchange_token,tradingsymbol,name,last_price,expiry,strike,tick_size,lot_size,instrument_type,segment,exchange
256265,1001,NIFTY 50,NIFTY 50,0,,0,0.05,1,EQ,INDICES,NSE
408065,1002,INFY,INFOSYS,0,,0,0.05,1,EQ,NSE,NSE
2002,2002,NIFTY26JULFUT,NIFTY,0,2026-07-30,0,0.05,75,FUT,NFO-FUT,NFO
2001,2001,NIFTY26JUNFUT,NIFTY,0,2026-06-25,0,0.05,75,FUT,NFO-FUT,NFO
3001,3001,NIFTY26JUN20000CE,NIFTY,0,2026-06-25,20000,0.05,75,CE,NFO-OPT,NFO
3002,3002,NIFTY26JUN20000PE,NIFTY,0,2026-06-25,20000,0.05,75,PE,NFO-OPT,NFO
3003,3003,NIFTY26JUN20100CE,NIFTY,0,2026-06-25,20100,0.05,75,CE,NFO-OPT,NFO
`

func TestClientCatalogQueries(t *testing.T) {
	ctx := context.Background()
	transport := &catalogTransport{body: catalogCSV}
	client := openTestClient(t, ctx, transport, filepath.Join(t.TempDir(), "catalog.duckdb"))

	listed, err := client.List(ctx, " nse ", "NSE")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got := instrumentIDs(listed); !reflect.DeepEqual(got, []instrumentcatalog.InstrumentID{"NSE:INFY", "NSE:NIFTY 50"}) {
		t.Fatalf("List(NSE) IDs = %v", got)
	}

	instrument, err := client.Get(ctx, "nse:nifty 50")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if instrument.InstrumentToken != 256265 || instrument.OptionsCount != 3 || instrument.FuturesCount != 2 {
		t.Fatalf("Get() = %#v", instrument)
	}
	byToken, err := client.GetByToken(ctx, 408065)
	if err != nil || byToken.ID != "NSE:INFY" {
		t.Fatalf("GetByToken() = %#v, %v", byToken, err)
	}
	if _, err := client.Get(ctx, "NSE:MISSING"); !errors.Is(err, instrumentcatalog.ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := client.GetByToken(ctx, 0); !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("GetByToken(0) error = %v, want ErrInvalidInput", err)
	}

	searchResults, err := client.Search(ctx, "nifty 20000 call", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ID != "NFO:NIFTY26JUN20000CE" {
		t.Fatalf("Search() = %#v", searchResults)
	}
	if _, err := client.Search(ctx, " ", 0); !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("Search(empty) error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.Search(ctx, "::--", 0); !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("Search(punctuation) error = %v, want ErrInvalidInput", err)
	}
	wildcardResults, err := client.Search(ctx, "%", 10)
	if err != nil || len(wildcardResults) != 0 {
		t.Fatalf("Search(literal wildcard) = %#v, %v", wildcardResults, err)
	}

	fo, err := client.ListFO(ctx, "NSE")
	if err != nil || len(fo) != 1 || fo[0].ID != "NSE:NIFTY 50" {
		t.Fatalf("ListFO() = %#v, %v", fo, err)
	}
	underlyings, err := client.ListUnderlyingIDs(ctx, "NFO")
	if err != nil || !reflect.DeepEqual(underlyings, []instrumentcatalog.InstrumentID{"NSE:NIFTY 50"}) {
		t.Fatalf("ListUnderlyingIDs() = %v, %v", underlyings, err)
	}

	futures, err := client.GetFutures(ctx, instrumentcatalog.FuturesFilter{
		UnderlyingID:  "NSE:NIFTY 50",
		ExpiryNumbers: []int{0},
	})
	if err != nil || len(futures) != 1 || futures[0].ID != "NFO:NIFTY26JUNFUT" {
		t.Fatalf("GetFutures() = %#v, %v", futures, err)
	}
	options, err := client.GetOptions(ctx, instrumentcatalog.OptionsFilter{
		UnderlyingID: "NSE:NIFTY 50",
		ExpiryDates:  []time.Time{instrumentDate(2026, time.June, 25)},
		Strikes:      &instrumentcatalog.StrikeRange{Min: floatPointer(19900), Max: floatPointer(20050)},
		Types:        []instrumentcatalog.OptionType{instrumentcatalog.OptionTypeCall},
	})
	if err != nil || len(options) != 1 || options[0].ID != "NFO:NIFTY26JUN20000CE" {
		t.Fatalf("GetOptions() = %#v, %v", options, err)
	}
	allOptions, err := client.GetOptions(ctx, instrumentcatalog.OptionsFilter{UnderlyingID: "NSE:NIFTY 50"})
	if err != nil {
		t.Fatalf("GetOptions(all) error = %v", err)
	}
	if got := instrumentIDs(allOptions); !reflect.DeepEqual(got, []instrumentcatalog.InstrumentID{
		"NFO:NIFTY26JUN20000CE",
		"NFO:NIFTY26JUN20000PE",
		"NFO:NIFTY26JUN20100CE",
	}) {
		t.Fatalf("GetOptions(all) order = %v", got)
	}

	index, err := client.TokenIndex(ctx)
	if err != nil {
		t.Fatalf("TokenIndex() error = %v", err)
	}
	if index.Len() != 7 {
		t.Fatalf("TokenIndex().Len() = %d, want 7", index.Len())
	}
	if token, ok := index.Token("nse:infy"); !ok || token != 408065 {
		t.Fatalf("TokenIndex().Token() = %d, %v", token, ok)
	}
	if id, ok := index.ID(3002); !ok || id != "NFO:NIFTY26JUN20000PE" {
		t.Fatalf("TokenIndex().ID() = %q, %v", id, ok)
	}
}

func TestSearchUsesDefaultAndExplicitLimits(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("instrument_token,exchange_token,tradingsymbol,name,last_price,expiry,strike,tick_size,lot_size,instrument_type,segment,exchange\n")
	for i := 1; i <= 60; i++ {
		_, _ = fmt.Fprintf(&csv, "%d,%d,MATCH%02d,MATCH COMPANY %02d,0,,0,0.05,1,EQ,NSE,NSE\n", i, i, i, i)
	}
	ctx := context.Background()
	client := openTestClient(t, ctx, &catalogTransport{body: csv.String()}, filepath.Join(t.TempDir(), "catalog.duckdb"))
	results, err := client.Search(ctx, "match", 0)
	if err != nil || len(results) != instrumentcatalog.DefaultSearchLimit {
		t.Fatalf("Search(default limit) returned %d rows, %v", len(results), err)
	}
	results, err = client.Search(ctx, "match", 7)
	if err != nil || len(results) != 7 {
		t.Fatalf("Search(explicit limit) returned %d rows, %v", len(results), err)
	}
}

func TestClientRefreshLifecycle(t *testing.T) {
	ctx := context.Background()
	transport := &catalogTransport{body: catalogCSV}
	client, repository := openTestClientWithRepository(t, ctx, transport, filepath.Join(t.TempDir(), "catalog.duckdb"))
	if got := transport.callCount(); got != 1 {
		t.Fatalf("constructor fetch calls = %d, want 1", got)
	}

	lastRefreshed, err := client.LastRefreshedAt(ctx)
	if err != nil || lastRefreshed.IsZero() {
		t.Fatalf("LastRefreshedAt() = %v, %v", lastRefreshed, err)
	}
	refreshed, err := client.RefreshIfStale(ctx)
	if err != nil || refreshed {
		t.Fatalf("same-day RefreshIfStale() = %v, %v", refreshed, err)
	}
	if got := transport.callCount(); got != 1 {
		t.Fatalf("same-day fetch calls = %d, want 1", got)
	}

	catalog, err := repository.List(ctx, nil)
	if err != nil {
		t.Fatalf("List() before aging repository error = %v", err)
	}
	if err := repository.Replace(ctx, catalog, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("Replace() to age repository error = %v", err)
	}
	type refreshResult struct {
		refreshed bool
		err       error
	}
	results := make(chan refreshResult, 8)
	for range 8 {
		go func() {
			refreshed, err := client.RefreshIfStale(ctx)
			results <- refreshResult{refreshed: refreshed, err: err}
		}()
	}
	refreshCount := 0
	for range 8 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent next-day RefreshIfStale() error = %v", result.err)
		}
		if result.refreshed {
			refreshCount++
		}
	}
	if refreshCount != 1 {
		t.Fatalf("concurrent refresh count = %d, want 1", refreshCount)
	}
	if got := transport.callCount(); got != 2 {
		t.Fatalf("next-day fetch calls = %d, want 2", got)
	}
	if err := client.Refresh(ctx); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if got := transport.callCount(); got != 3 {
		t.Fatalf("forced refresh calls = %d, want 3", got)
	}
}

func TestClientReopensFreshCatalogWithoutFetching(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.duckdb")
	firstTransport := &catalogTransport{body: catalogCSV}
	first := openTestClient(t, ctx, firstTransport, path)
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	secondTransport := &catalogTransport{err: errors.New("source must not be called")}
	second, err := newClientAtPath(ctx, newKiteClient(secondTransport), path)
	if err != nil {
		t.Fatalf("reopen NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if got := secondTransport.callCount(); got != 0 {
		t.Fatalf("reopen fetch calls = %d, want 0", got)
	}
	if instrument, err := second.Get(ctx, "NSE:INFY"); err != nil || instrument.InstrumentToken != 408065 {
		t.Fatalf("reopened Get() = %#v, %v", instrument, err)
	}
}

func TestFailedRefreshPreservesCatalog(t *testing.T) {
	ctx := context.Background()
	transport := &catalogTransport{body: catalogCSV}
	client, repository := openTestClientWithRepository(t, ctx, transport, filepath.Join(t.TempDir(), "catalog.duckdb"))

	transport.setBody(`instrument_token,exchange_token,tradingsymbol,name,last_price,expiry,strike,tick_size,lot_size,instrument_type,segment,exchange
1,11,INFY,INFOSYS,0,,0,0.05,1,EQ,NSE,NSE
2,12,INFY,INFOSYS,0,,0,0.05,1,EQ,NSE,NSE
`)
	if err := client.Refresh(ctx); !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("invalid Refresh() error = %v, want ErrInvalidInput", err)
	}
	if instrument, err := client.Get(ctx, "NSE:INFY"); err != nil || instrument.InstrumentToken != 408065 {
		t.Fatalf("Get() after invalid refresh = %#v, %v", instrument, err)
	}

	transport.setError(errors.New("network unavailable"))
	if err := client.Refresh(ctx); err == nil {
		t.Fatal("source-failed Refresh() returned nil")
	}
	if instrument, err := client.Get(ctx, "NSE:NIFTY 50"); err != nil || instrument.InstrumentToken != 256265 {
		t.Fatalf("Get() after source failure = %#v, %v", instrument, err)
	}

	valid, err := repository.List(ctx, nil)
	if err != nil {
		t.Fatalf("load replacement fixture: %v", err)
	}
	valid = valid[:1]
	duplicate := valid[0]
	duplicate.InstrumentToken++
	valid = append(valid, duplicate)
	if err := repository.Replace(ctx, valid, time.Now()); err == nil {
		t.Fatal("constraint-failed replace() returned nil")
	}
	if instrument, err := client.Get(ctx, "NSE:INFY"); err != nil || instrument.InstrumentToken != 408065 {
		t.Fatalf("Get() after write failure = %#v, %v", instrument, err)
	}
}

func TestNewClientRejectsLegacyDatabaseWithoutModification(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.duckdb")
	db, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE instruments (id VARCHAR); INSERT INTO instruments VALUES ('NSE:INFY')"); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	repository, err := NewDuckDB(ctx, path)
	if repository != nil || !errors.Is(err, instrumentcatalog.ErrIncompatibleDatabase) {
		t.Fatalf("NewDuckDB(legacy) = %#v, %v", repository, err)
	}

	db, err = sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("reopen legacy database: %v", err)
	}
	defer db.Close()
	var id string
	if err := db.QueryRow("SELECT id FROM instruments").Scan(&id); err != nil || id != "NSE:INFY" {
		t.Fatalf("legacy row after rejection = %q, %v", id, err)
	}
	var markerCount int
	if err := db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_name = 'instrument_catalog_state'").Scan(&markerCount); err != nil {
		t.Fatalf("inspect legacy marker: %v", err)
	}
	if markerCount != 0 {
		t.Fatalf("legacy database marker count = %d, want 0", markerCount)
	}
}

func TestNewClientCanRetryAfterInitialSourceFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.duckdb")
	transport := &catalogTransport{body: "instrument_token,exchange_token,tradingsymbol,name,last_price,expiry,strike,tick_size,lot_size,instrument_type,segment,exchange\n"}
	client, err := newClientAtPath(ctx, newKiteClient(transport), path)
	if client != nil || !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("first NewClient() = %#v, %v", client, err)
	}

	transport.setBody(catalogCSV)
	client, err = newClientAtPath(ctx, newKiteClient(transport), path)
	if err != nil {
		t.Fatalf("retry NewClient() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestNewClientValidatesArgumentsAndSchemaVersion(t *testing.T) {
	ctx := context.Background()
	transport := &catalogTransport{body: catalogCSV}
	if client, err := instrumentcatalog.NewClient(ctx, nil, nil); client != nil || !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("NewClient(nil Kite client) = %#v, %v", client, err)
	}
	if client, err := instrumentcatalog.NewClient(nil, newKiteClient(transport), nil); client != nil || !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("NewClient(nil context) = %#v, %v", client, err)
	}
	if repository, err := NewDuckDB(ctx, t.TempDir()); repository != nil || !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("NewDuckDB(directory) = %#v, %v", repository, err)
	}

	path := filepath.Join(t.TempDir(), "future-schema.duckdb")
	client := openTestClient(t, ctx, transport, path)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatalf("open catalog to change version: %v", err)
	}
	if _, err := db.Exec("UPDATE instrument_catalog_state SET schema_version = 2 WHERE id = 1"); err != nil {
		t.Fatalf("update schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close versioned catalog: %v", err)
	}
	client, err = newClientAtPath(ctx, newKiteClient(transport), path)
	if client != nil || !errors.Is(err, instrumentcatalog.ErrIncompatibleDatabase) {
		t.Fatalf("NewClient(unsupported schema) = %#v, %v", client, err)
	}
}

func TestClientHonorsCancelledContext(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t, ctx, &catalogTransport{body: catalogCSV}, filepath.Join(t.TempDir(), "catalog.duckdb"))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.List(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(cancelled) error = %v", err)
	}
	if err := client.Refresh(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh(cancelled) error = %v", err)
	}
	if _, err := client.List(nil); !errors.Is(err, instrumentcatalog.ErrInvalidInput) {
		t.Fatalf("List(nil) error = %v", err)
	}
}

type catalogTransport struct {
	mu    sync.Mutex
	body  string
	err   error
	calls int
}

func (t *catalogTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	if t.err != nil {
		return nil, t.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func (t *catalogTransport) setBody(body string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.body = body
	t.err = nil
}

func (t *catalogTransport) setError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.err = err
}

func (t *catalogTransport) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

func newKiteClient(transport http.RoundTripper) *kiteconnect.Client {
	client := kiteconnect.New("test-api-key")
	client.SetHTTPClient(&http.Client{Transport: transport})
	return client
}

func openTestClient(t *testing.T, ctx context.Context, transport *catalogTransport, path string) *instrumentcatalog.Client {
	t.Helper()
	client, _ := openTestClientWithRepository(t, ctx, transport, path)
	return client
}

func openTestClientWithRepository(t *testing.T, ctx context.Context, transport *catalogTransport, path string) (*instrumentcatalog.Client, *DuckDB) {
	t.Helper()
	repository, err := NewDuckDB(ctx, path)
	if err != nil {
		t.Fatalf("NewDuckDB() error = %v", err)
	}
	client, err := instrumentcatalog.NewClient(ctx, newKiteClient(transport), repository)
	if err != nil {
		_ = repository.Close()
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, repository
}

func newClientAtPath(ctx context.Context, kiteClient *kiteconnect.Client, path string) (*instrumentcatalog.Client, error) {
	repository, err := NewDuckDB(ctx, path)
	if err != nil {
		return nil, err
	}
	client, err := instrumentcatalog.NewClient(ctx, kiteClient, repository)
	if err != nil {
		_ = repository.Close()
		return nil, err
	}
	return client, nil
}

func instrumentIDs(instruments []instrumentcatalog.Instrument) []instrumentcatalog.InstrumentID {
	ids := make([]instrumentcatalog.InstrumentID, len(instruments))
	for i, instrument := range instruments {
		ids[i] = instrument.ID
	}
	return ids
}

func floatPointer(value float64) *float64 { return &value }

func instrumentDate(year int, month time.Month, day int) time.Time {
	india := time.FixedZone("Asia/Kolkata", 5*60*60+30*60)
	return time.Date(year, month, day, 0, 0, 0, 0, india)
}
