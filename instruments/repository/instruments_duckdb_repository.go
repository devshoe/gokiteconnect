package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/devshoe/gokiteconnect/instruments"
	_ "github.com/duckdb/duckdb-go/v2"
)

const catalogSchemaVersion = 1

const instrumentColumns = `
	id,
	exchange,
	trading_symbol,
	instrument_token,
	exchange_token,
	name,
	display_name,
	search_string,
	expiry,
	expiry_number,
	strike,
	tick_size,
	lot_size,
	instrument_type,
	segment,
	is_fo,
	underlying_id,
	underlying_is_listed,
	options_count,
	futures_count
`

var requiredInstrumentColumns = []string{
	"id",
	"exchange",
	"trading_symbol",
	"instrument_token",
	"exchange_token",
	"name",
	"display_name",
	"search_string",
	"search_key",
	"expiry",
	"expiry_number",
	"strike",
	"tick_size",
	"lot_size",
	"instrument_type",
	"segment",
	"is_fo",
	"underlying_id",
	"underlying_is_listed",
	"options_count",
	"futures_count",
}

// DuckDB stores a normalized instrument catalog in a package-owned DuckDB schema.
type DuckDB struct {
	db *sql.DB
}

var _ instruments.Repository = (*DuckDB)(nil)

// NewDuckDB opens databasePath. The path must be new or point to a database
// previously created by this repository.
func NewDuckDB(ctx context.Context, databasePath string) (*DuckDB, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", instruments.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	databasePath = strings.TrimSpace(databasePath)
	if databasePath == "" {
		return nil, fmt.Errorf("%w: database path is required", instruments.ErrInvalidInput)
	}

	isMemory := databasePath == ":memory:"
	existed := false
	if !isMemory {
		info, err := os.Stat(databasePath)
		switch {
		case err == nil:
			if info.IsDir() {
				return nil, fmt.Errorf("%w: database path is a directory", instruments.ErrInvalidInput)
			}
			existed = true
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("instruments: inspect database path: %w", err)
		}
	}

	db, err := sql.Open("duckdb", databasePath)
	if err != nil {
		return nil, fmt.Errorf("instruments: open DuckDB: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("instruments: connect to DuckDB: %w", err)
	}

	store := &DuckDB{db: db}
	if existed {
		if err := store.validateSchema(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
		return store, nil
	}
	if err := store.initializeSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *DuckDB) initializeSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("instruments: begin schema initialization: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE instrument_catalog_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			schema_version INTEGER NOT NULL,
			refreshed_at TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("instruments: create catalog state: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO instrument_catalog_state (id, schema_version, refreshed_at) VALUES (1, ?, NULL)",
		catalogSchemaVersion,
	); err != nil {
		return fmt.Errorf("instruments: initialize catalog state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, createInstrumentTableSQL("instruments")); err != nil {
		return fmt.Errorf("instruments: create catalog table: %w", err)
	}
	if err := createInstrumentIndexes(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("instruments: commit schema initialization: %w", err)
	}
	return nil
}

func (s *DuckDB) validateSchema(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx,
		"SELECT schema_version FROM instrument_catalog_state WHERE id = 1",
	).Scan(&version); err != nil {
		return fmt.Errorf("%w: missing package schema marker", instruments.ErrIncompatibleDatabase)
	}
	if version != catalogSchemaVersion {
		return fmt.Errorf("%w: schema version %d, expected %d", instruments.ErrIncompatibleDatabase, version, catalogSchemaVersion)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_name = 'instruments'
	`)
	if err != nil {
		return fmt.Errorf("%w: inspect instruments table: %v", instruments.ErrIncompatibleDatabase, err)
	}
	defer rows.Close()
	columns := make(map[string]struct{}, len(requiredInstrumentColumns))
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return fmt.Errorf("%w: inspect instruments columns: %v", instruments.ErrIncompatibleDatabase, err)
		}
		columns[column] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: inspect instruments columns: %v", instruments.ErrIncompatibleDatabase, err)
	}
	for _, required := range requiredInstrumentColumns {
		if _, ok := columns[required]; !ok {
			return fmt.Errorf("%w: instruments table is missing column %q", instruments.ErrIncompatibleDatabase, required)
		}
	}
	return nil
}

// Replace atomically replaces the complete catalog snapshot.
func (s *DuckDB) Replace(ctx context.Context, catalog []instruments.Instrument, refreshedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("instruments: begin refresh: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS instruments_next"); err != nil {
		return fmt.Errorf("instruments: clear refresh staging table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, createInstrumentTableSQL("instruments_next")); err != nil {
		return fmt.Errorf("instruments: create refresh staging table: %w", err)
	}

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO instruments_next (
			id, exchange, trading_symbol, instrument_token, exchange_token,
			name, display_name, search_string, search_key, expiry, expiry_number,
			strike, tick_size, lot_size, instrument_type, segment, is_fo,
			underlying_id, underlying_is_listed, options_count, futures_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("instruments: prepare catalog insert: %w", err)
	}
	defer statement.Close()

	for _, instrument := range catalog {
		if _, err := statement.ExecContext(ctx,
			instrument.ID,
			instrument.Exchange,
			instrument.TradingSymbol,
			instrument.InstrumentToken,
			instrument.ExchangeToken,
			nullableString(instrument.Name),
			instrument.DisplayName,
			instrument.SearchString,
			strings.ToLower(instrument.SearchString),
			nullableDate(instrument.Expiry),
			nullableInt(instrument.ExpiryNumber),
			instrument.Strike,
			instrument.TickSize,
			instrument.LotSize,
			instrument.InstrumentType,
			instrument.Segment,
			instrument.IsFO,
			nullableInstrumentID(instrument.UnderlyingID),
			instrument.UnderlyingIsListed,
			instrument.OptionsCount,
			instrument.FuturesCount,
		); err != nil {
			return fmt.Errorf("instruments: insert %s: %w", instrument.ID, err)
		}
	}
	if err := statement.Close(); err != nil {
		return fmt.Errorf("instruments: finish catalog inserts: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DROP TABLE instruments"); err != nil {
		return fmt.Errorf("instruments: replace old catalog: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE instruments_next RENAME TO instruments"); err != nil {
		return fmt.Errorf("instruments: activate refreshed catalog: %w", err)
	}
	if err := createInstrumentIndexes(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE instrument_catalog_state SET refreshed_at = ? WHERE id = 1",
		refreshedAt.UTC(),
	); err != nil {
		return fmt.Errorf("instruments: record refresh time: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("instruments: commit refresh: %w", err)
	}
	return nil
}

// LastRefreshedAt returns the time of the last successful replacement.
func (s *DuckDB) LastRefreshedAt(ctx context.Context) (time.Time, error) {
	var refreshedAt sql.NullTime
	if err := s.db.QueryRowContext(ctx,
		"SELECT refreshed_at FROM instrument_catalog_state WHERE id = 1",
	).Scan(&refreshedAt); err != nil {
		return time.Time{}, fmt.Errorf("instruments: read refresh time: %w", err)
	}
	if !refreshedAt.Valid {
		return time.Time{}, nil
	}
	return refreshedAt.Time, nil
}

// List returns the catalog, optionally restricted to exchanges.
func (s *DuckDB) List(ctx context.Context, exchanges []string) ([]instruments.Instrument, error) {
	query := "SELECT " + instrumentColumns + " FROM instruments"
	args := make([]any, 0, len(exchanges))
	if len(exchanges) > 0 {
		query += " WHERE exchange IN (" + placeholders(len(exchanges)) + ")"
		for _, exchange := range exchanges {
			args = append(args, exchange)
		}
	}
	query += " ORDER BY exchange, display_name, id"
	return s.queryInstruments(ctx, query, args...)
}

// Get returns one instrument by ID.
func (s *DuckDB) Get(ctx context.Context, id instruments.InstrumentID) (instruments.Instrument, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+instrumentColumns+" FROM instruments WHERE id = ?",
		id,
	)
	return scanInstrument(row)
}

// GetByToken returns one instrument by token.
func (s *DuckDB) GetByToken(ctx context.Context, token int64) (instruments.Instrument, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+instrumentColumns+" FROM instruments WHERE instrument_token = ?",
		token,
	)
	return scanInstrument(row)
}

// ListFO returns listed underlyings with active futures or options.
func (s *DuckDB) ListFO(ctx context.Context, exchanges []string) ([]instruments.Instrument, error) {
	query := "SELECT " + instrumentColumns + `
		FROM instruments
		WHERE (options_count > 0 OR futures_count > 0)
			AND is_fo = false
			AND underlying_id IS NULL`
	args := make([]any, 0, len(exchanges))
	if len(exchanges) > 0 {
		query += " AND exchange IN (" + placeholders(len(exchanges)) + ")"
		for _, exchange := range exchanges {
			args = append(args, exchange)
		}
	}
	query += " ORDER BY exchange, display_name, id"
	return s.queryInstruments(ctx, query, args...)
}

// ListUnderlyingIDs returns distinct derivative underlying IDs.
func (s *DuckDB) ListUnderlyingIDs(ctx context.Context, exchanges []string) ([]instruments.InstrumentID, error) {
	query := "SELECT DISTINCT underlying_id FROM instruments WHERE underlying_id IS NOT NULL"
	args := make([]any, 0, len(exchanges))
	if len(exchanges) > 0 {
		query += " AND exchange IN (" + placeholders(len(exchanges)) + ")"
		for _, exchange := range exchanges {
			args = append(args, exchange)
		}
	}
	query += " ORDER BY underlying_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("instruments: list underlying IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]instruments.InstrumentID, 0)
	for rows.Next() {
		var id instruments.InstrumentID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("instruments: scan underlying ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("instruments: list underlying IDs: %w", err)
	}
	return ids, nil
}

// GetFutures returns futures matching filter.
func (s *DuckDB) GetFutures(ctx context.Context, filter instruments.FuturesFilter) ([]instruments.Instrument, error) {
	query := "SELECT " + instrumentColumns + " FROM instruments WHERE underlying_id = ? AND instrument_type = 'FUT'"
	args := []any{filter.UnderlyingID}
	query, args = appendExpiryFilters(query, args, filter.ExpiryNumbers, filter.ExpiryDates)
	query += " ORDER BY expiry, id"
	return s.queryInstruments(ctx, query, args...)
}

// GetOptions returns options matching filter.
func (s *DuckDB) GetOptions(ctx context.Context, filter instruments.OptionsFilter) ([]instruments.Instrument, error) {
	query := "SELECT " + instrumentColumns + " FROM instruments WHERE underlying_id = ?"
	args := []any{filter.UnderlyingID}
	if len(filter.Types) == 0 {
		query += " AND instrument_type IN ('CE', 'PE')"
	} else {
		query += " AND instrument_type IN (" + placeholders(len(filter.Types)) + ")"
		for _, optionType := range filter.Types {
			args = append(args, optionType)
		}
	}
	query, args = appendExpiryFilters(query, args, filter.ExpiryNumbers, filter.ExpiryDates)
	if filter.Strikes != nil {
		if filter.Strikes.Min != nil {
			query += " AND strike >= ?"
			args = append(args, *filter.Strikes.Min)
		}
		if filter.Strikes.Max != nil {
			query += " AND strike <= ?"
			args = append(args, *filter.Strikes.Max)
		}
	}
	query += " ORDER BY expiry, strike, instrument_type, id"
	return s.queryInstruments(ctx, query, args...)
}

// Search returns instruments ranked by exact, prefix, and tokenized matches.
func (s *DuckDB) Search(ctx context.Context, queryText string, limit int) ([]instruments.Instrument, error) {
	escapedQuery := escapeLike(queryText)
	pattern := "%" + escapedQuery + "%"
	prefix := escapedQuery + "%"
	terms := searchTerms(queryText)
	termPredicates := make([]string, 0, len(terms))
	args := []any{pattern, pattern, pattern, pattern}
	for _, term := range terms {
		termPredicates = append(termPredicates, "search_key LIKE ? ESCAPE '$'")
		args = append(args, "%"+escapeLike(term)+"%")
	}
	args = append(args, queryText, queryText, prefix, prefix, prefix, prefix, limit)

	query := `SELECT ` + instrumentColumns + `
		FROM instruments
		WHERE id ILIKE ? ESCAPE '$'
			OR name ILIKE ? ESCAPE '$'
			OR underlying_id ILIKE ? ESCAPE '$'
			OR search_string ILIKE ? ESCAPE '$'
			OR (` + strings.Join(termPredicates, " AND ") + `)
		ORDER BY
			CASE
				WHEN lower(display_name) = lower(?) THEN 0
				WHEN lower(trading_symbol) = lower(?) THEN 1
				WHEN display_name ILIKE ? ESCAPE '$' THEN 2
				WHEN trading_symbol ILIKE ? ESCAPE '$' THEN 3
				WHEN id ILIKE ? ESCAPE '$' THEN 4
				WHEN name ILIKE ? ESCAPE '$' THEN 5
				ELSE 6
			END,
			length(display_name),
			trading_symbol,
			id
		LIMIT ?`
	return s.queryInstruments(ctx, query, args...)
}

func (s *DuckDB) queryInstruments(ctx context.Context, query string, args ...any) ([]instruments.Instrument, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("instruments: query catalog: %w", err)
	}
	defer rows.Close()
	catalog := make([]instruments.Instrument, 0)
	for rows.Next() {
		instrument, err := scanInstrument(rows)
		if err != nil {
			return nil, err
		}
		catalog = append(catalog, instrument)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("instruments: query catalog: %w", err)
	}
	return catalog, nil
}

// Close checkpoints and closes the database.
func (s *DuckDB) Close() error {
	checkpointErr := func() error {
		_, err := s.db.Exec("CHECKPOINT")
		return err
	}()
	return errors.Join(checkpointErr, s.db.Close())
}

type scanner interface {
	Scan(dest ...any) error
}

func scanInstrument(row scanner) (instruments.Instrument, error) {
	var (
		instrument   instruments.Instrument
		name         sql.NullString
		expiry       sql.NullTime
		expiryNumber sql.NullInt64
		underlyingID sql.NullString
	)
	err := row.Scan(
		&instrument.ID,
		&instrument.Exchange,
		&instrument.TradingSymbol,
		&instrument.InstrumentToken,
		&instrument.ExchangeToken,
		&name,
		&instrument.DisplayName,
		&instrument.SearchString,
		&expiry,
		&expiryNumber,
		&instrument.Strike,
		&instrument.TickSize,
		&instrument.LotSize,
		&instrument.InstrumentType,
		&instrument.Segment,
		&instrument.IsFO,
		&underlyingID,
		&instrument.UnderlyingIsListed,
		&instrument.OptionsCount,
		&instrument.FuturesCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return instruments.Instrument{}, instruments.ErrNotFound
	}
	if err != nil {
		return instruments.Instrument{}, fmt.Errorf("instruments: scan catalog row: %w", err)
	}
	if name.Valid {
		instrument.Name = stringPointer(name.String)
	}
	if expiry.Valid {
		instrument.Expiry = timePointer(normalizeDate(expiry.Time))
	}
	if expiryNumber.Valid {
		instrument.ExpiryNumber = intPointer(int(expiryNumber.Int64))
	}
	if underlyingID.Valid {
		id := instruments.InstrumentID(underlyingID.String)
		instrument.UnderlyingID = &id
	}
	return instrument, nil
}

func createInstrumentTableSQL(table string) string {
	return fmt.Sprintf(`
		CREATE TABLE %s (
			id VARCHAR PRIMARY KEY,
			exchange VARCHAR NOT NULL,
			trading_symbol VARCHAR NOT NULL,
			instrument_token BIGINT NOT NULL UNIQUE,
			exchange_token VARCHAR NOT NULL,
			name VARCHAR,
			display_name VARCHAR NOT NULL,
			search_string VARCHAR NOT NULL,
			search_key VARCHAR NOT NULL,
			expiry DATE,
			expiry_number INTEGER,
			strike DOUBLE NOT NULL,
			tick_size DOUBLE NOT NULL,
			lot_size INTEGER NOT NULL,
			instrument_type VARCHAR NOT NULL,
			segment VARCHAR NOT NULL,
			is_fo BOOLEAN NOT NULL,
			underlying_id VARCHAR,
			underlying_is_listed BOOLEAN NOT NULL,
			options_count INTEGER NOT NULL,
			futures_count INTEGER NOT NULL
		)
	`, table)
}

func createInstrumentIndexes(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		"CREATE INDEX idx_instruments_underlying ON instruments (underlying_id)",
		"CREATE INDEX idx_instruments_exchange ON instruments (exchange)",
		"CREATE INDEX idx_instruments_expiry ON instruments (underlying_id, expiry, instrument_type)",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("instruments: create catalog index: %w", err)
		}
	}
	return nil
}

func appendExpiryFilters(query string, args []any, numbers []int, dates []time.Time) (string, []any) {
	if len(numbers) > 0 {
		query += " AND expiry_number IN (" + placeholders(len(numbers)) + ")"
		for _, number := range numbers {
			args = append(args, number)
		}
	}
	if len(dates) > 0 {
		query += " AND strftime(expiry, '%Y-%m-%d') IN (" + placeholders(len(dates)) + ")"
		for _, date := range dates {
			args = append(args, normalizeDate(date).Format(time.DateOnly))
		}
	}
	return query, args
}

func placeholders(count int) string {
	values := make([]string, count)
	for i := range values {
		values[i] = "?"
	}
	return strings.Join(values, ", ")
}

func escapeLike(value string) string {
	return strings.NewReplacer("$", "$$", "%", "$%", "_", "$_").Replace(value)
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableDate(value *time.Time) any {
	if value == nil {
		return nil
	}
	return dateKey(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInstrumentID(value *instruments.InstrumentID) any {
	if value == nil {
		return nil
	}
	return *value
}

var indiaLocation = time.FixedZone("Asia/Kolkata", 5*60*60+30*60)

func normalizeDate(value time.Time) time.Time {
	inIndia := value.In(indiaLocation)
	return time.Date(inIndia.Year(), inIndia.Month(), inIndia.Day(), 0, 0, 0, 0, indiaLocation)
}

func dateKey(value time.Time) string {
	return value.In(indiaLocation).Format(time.DateOnly)
}

func searchTerms(query string) []string {
	normalized := strings.NewReplacer(":", " ", "-", " ", "_", " ", "/", " ").Replace(strings.ToLower(query))
	seen := make(map[string]struct{})
	terms := make([]string, 0)
	for _, term := range strings.Fields(normalized) {
		if _, ok := seen[term]; !ok {
			seen[term] = struct{}{}
			terms = append(terms, term)
		}
		switch term {
		case "call":
			if _, ok := seen["ce"]; !ok {
				seen["ce"] = struct{}{}
				terms = append(terms, "ce")
			}
		case "put":
			if _, ok := seen["pe"]; !ok {
				seen["pe"] = struct{}{}
				terms = append(terms, "pe")
			}
		}
	}
	return terms
}

func stringPointer(value string) *string { return &value }

func timePointer(value time.Time) *time.Time { return &value }

func intPointer(value int) *int { return &value }
