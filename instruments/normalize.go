package instruments

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	kiteconnect "github.com/devshoe/gokiteconnect"
)

/*
this file normalizes the raw csv response to richer information and data to support searching and such
*/

var indiaLocation = time.FixedZone("Asia/Kolkata", 5*60*60+30*60)

type contractCounts struct {
	options int
	futures int
}

func normalizeSnapshot(source kiteconnect.Instruments) ([]Instrument, error) {
	if len(source) == 0 {
		return nil, fmt.Errorf("%w: instrument snapshot is empty", ErrInvalidInput)
	}

	instruments := make([]Instrument, 0, len(source))
	ids := make(map[InstrumentID]struct{}, len(source))
	tokens := make(map[int64]struct{}, len(source))

	for rowNumber, sourceInstrument := range source {
		instrument, err := normalizeSourceInstrument(sourceInstrument)
		if err != nil {
			return nil, fmt.Errorf("instrument row %d: %w", rowNumber+1, err)
		}
		if _, exists := ids[instrument.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate instrument ID %s", ErrInvalidInput, instrument.ID)
		}
		if _, exists := tokens[instrument.InstrumentToken]; exists {
			return nil, fmt.Errorf("%w: duplicate instrument token %d", ErrInvalidInput, instrument.InstrumentToken)
		}
		ids[instrument.ID] = struct{}{}
		tokens[instrument.InstrumentToken] = struct{}{}
		instruments = append(instruments, instrument)
	}

	expiries := make(map[InstrumentID]map[string]time.Time)
	counts := make(map[InstrumentID]contractCounts)
	for i := range instruments {
		instrument := &instruments[i]
		if instrument.UnderlyingID == nil {
			continue
		}
		instrument.UnderlyingIsListed = containsID(ids, *instrument.UnderlyingID)
		count := counts[*instrument.UnderlyingID]
		switch instrument.InstrumentType {
		case string(OptionTypeCall), string(OptionTypePut):
			count.options++
		case "FUT":
			count.futures++
		}
		counts[*instrument.UnderlyingID] = count

		if instrument.Expiry != nil {
			if expiries[*instrument.UnderlyingID] == nil {
				expiries[*instrument.UnderlyingID] = make(map[string]time.Time)
			}
			expiries[*instrument.UnderlyingID][dateKey(*instrument.Expiry)] = *instrument.Expiry
		}
	}

	expiryNumbers := make(map[InstrumentID]map[string]int, len(expiries))
	for underlyingID, datesByKey := range expiries {
		dates := make([]time.Time, 0, len(datesByKey))
		for _, date := range datesByKey {
			dates = append(dates, date)
		}
		sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
		expiryNumbers[underlyingID] = make(map[string]int, len(dates))
		for number, date := range dates {
			expiryNumbers[underlyingID][dateKey(date)] = number
		}
	}

	for i := range instruments {
		instrument := &instruments[i]
		countKey := instrument.ID
		if instrument.UnderlyingID != nil {
			countKey = *instrument.UnderlyingID
		}
		instrument.OptionsCount = counts[countKey].options
		instrument.FuturesCount = counts[countKey].futures
		if instrument.UnderlyingID != nil && instrument.Expiry != nil {
			number := expiryNumbers[*instrument.UnderlyingID][dateKey(*instrument.Expiry)]
			instrument.ExpiryNumber = intPointer(number)
		}
		instrument.DisplayName = instrumentDisplayName(*instrument)
		instrument.SearchString = instrumentSearchString(*instrument)
	}

	return instruments, nil
}

func normalizeSourceInstrument(source kiteconnect.Instrument) (Instrument, error) {
	exchange := strings.ToUpper(strings.TrimSpace(source.Exchange))
	tradingSymbol := strings.ToUpper(strings.TrimSpace(source.Tradingsymbol))
	id, err := ParseInstrumentID(exchange + ":" + tradingSymbol)
	if err != nil {
		return Instrument{}, err
	}
	if source.InstrumentToken <= 0 {
		return Instrument{}, fmt.Errorf("%w: instrument token must be positive", ErrInvalidInput)
	}
	if source.ExchangeToken < 0 {
		return Instrument{}, fmt.Errorf("%w: exchange token cannot be negative", ErrInvalidInput)
	}
	if source.StrikePrice < 0 || source.TickSize < 0 {
		return Instrument{}, fmt.Errorf("%w: strike and tick size cannot be negative", ErrInvalidInput)
	}
	if source.LotSize < 0 || source.LotSize != math.Trunc(source.LotSize) || source.LotSize > math.MaxInt {
		return Instrument{}, fmt.Errorf("%w: lot size %v is not a non-negative integer", ErrInvalidInput, source.LotSize)
	}

	segment := strings.ToUpper(strings.TrimSpace(source.Segment))
	instrumentType := strings.ToUpper(strings.TrimSpace(source.InstrumentType))
	if segment == "INDICES" {
		instrumentType = "INDICES"
	}
	if instrumentType == "" {
		return Instrument{}, fmt.Errorf("%w: instrument type is required", ErrInvalidInput)
	}

	name := stringPointer(strings.TrimSpace(source.Name))
	isFO := instrumentType == string(OptionTypeCall) || instrumentType == string(OptionTypePut) || instrumentType == "FUT"
	var expiry *time.Time
	if !source.Expiry.IsZero() {
		expiry = timePointer(normalizeDate(source.Expiry.Time))
	}
	if isFO && expiry == nil {
		return Instrument{}, fmt.Errorf("%w: derivative %s has no expiry", ErrInvalidInput, id)
	}

	var underlyingID *InstrumentID
	if isFO {
		if name == nil {
			return Instrument{}, fmt.Errorf("%w: derivative %s has no underlying name", ErrInvalidInput, id)
		}
		underlying, err := deriveUnderlyingID(exchange, *name)
		if err != nil {
			return Instrument{}, err
		}
		underlyingID = &underlying
	}

	return Instrument{
		ID:              id,
		Exchange:        exchange,
		TradingSymbol:   tradingSymbol,
		InstrumentToken: int64(source.InstrumentToken),
		ExchangeToken:   strconv.Itoa(source.ExchangeToken),
		Name:            name,
		Expiry:          expiry,
		Strike:          source.StrikePrice,
		TickSize:        source.TickSize,
		LotSize:         int(source.LotSize),
		InstrumentType:  instrumentType,
		Segment:         segment,
		IsFO:            isFO,
		UnderlyingID:    underlyingID,
	}, nil
}

func deriveUnderlyingID(derivativesExchange, name string) (InstrumentID, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" {
		return "", fmt.Errorf("%w: underlying name is required", ErrInvalidInput)
	}

	var value string
	switch name {
	case "NIFTY":
		value = "NSE:NIFTY 50"
	case "BANKNIFTY":
		value = "NSE:NIFTY BANK"
	case "MIDCPNIFTY":
		value = "NSE:NIFTY MID SELECT"
	case "FINNIFTY":
		value = "NSE:NIFTY FIN SERVICE"
	default:
		switch derivativesExchange {
		case "NFO":
			value = "NSE:" + name
		case "BFO":
			value = "BSE:" + name
		default:
			value = derivativesExchange + ":" + name
		}
	}
	return ParseInstrumentID(value)
}

func instrumentDisplayName(instrument Instrument) string {
	base := instrument.TradingSymbol
	if instrument.Name != nil {
		base = *instrument.Name
	}
	if instrument.Expiry == nil {
		return instrument.TradingSymbol
	}

	expiry := instrument.Expiry.In(indiaLocation).Format("02 Jan 2006")
	switch instrument.InstrumentType {
	case "FUT":
		return strings.Join([]string{base, expiry, "Futures"}, " ")
	case string(OptionTypeCall):
		return strings.Join([]string{base, expiry, formatStrike(instrument.Strike), "Call"}, " ")
	case string(OptionTypePut):
		return strings.Join([]string{base, expiry, formatStrike(instrument.Strike), "Put"}, " ")
	default:
		return instrument.TradingSymbol
	}
}

func instrumentSearchString(instrument Instrument) string {
	parts := []string{
		instrument.DisplayName,
		instrument.Exchange,
		strconv.FormatInt(instrument.InstrumentToken, 10),
		string(instrument.ID),
		instrument.TradingSymbol,
		instrument.InstrumentType,
		instrument.Segment,
	}
	if instrument.Name != nil {
		parts = append(parts, *instrument.Name)
	}
	if instrument.UnderlyingID != nil {
		parts = append(parts, string(*instrument.UnderlyingID))
	}
	switch instrument.InstrumentType {
	case string(OptionTypeCall):
		parts = append(parts, "CE", "CALL")
	case string(OptionTypePut):
		parts = append(parts, "PE", "PUT")
	case "FUT":
		parts = append(parts, "FUT", "FUTURES")
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
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

func normalizeDate(value time.Time) time.Time {
	inIndia := value.In(indiaLocation)
	return time.Date(inIndia.Year(), inIndia.Month(), inIndia.Day(), 0, 0, 0, 0, indiaLocation)
}

func dateKey(value time.Time) string {
	return value.In(indiaLocation).Format(time.DateOnly)
}

func formatStrike(strike float64) string {
	return strconv.FormatFloat(strike, 'f', -1, 64)
}

func containsID(ids map[InstrumentID]struct{}, id InstrumentID) bool {
	_, ok := ids[id]
	return ok
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func timePointer(value time.Time) *time.Time { return &value }

func intPointer(value int) *int { return &value }

// equivalentSQL is the DuckDB equivalent of normalizeSnapshot for an already
// validated source_instruments relation containing the Kite instrument-master
// columns. Normalization stays in Go so refreshing never depends on DuckDB
// reading or validating external data; this query documents the transformation.
const equivalentSQL = `
WITH normalized_source AS (
	SELECT
		upper(trim(exchange)) AS exchange,
		upper(trim(tradingsymbol)) AS trading_symbol,
		instrument_token,
		CAST(exchange_token AS VARCHAR) AS exchange_token,
		nullif(trim(name), '') AS name,
		CAST(expiry AS DATE) AS expiry,
		strike,
		tick_size,
		lot_size,
		upper(trim(segment)) AS segment,
		upper(trim(instrument_type)) AS source_instrument_type
	FROM source_instruments
),
typed_source AS (
	SELECT
		*,
		CASE
			WHEN segment = 'INDICES' THEN 'INDICES'
			ELSE source_instrument_type
		END AS instrument_type
	FROM normalized_source
),
raw AS (
	SELECT
		exchange || ':' || trading_symbol AS id,
		exchange,
		trading_symbol,
		instrument_token,
		exchange_token,
		name,
		expiry,
		strike,
		tick_size,
		CAST(lot_size AS INTEGER) AS lot_size,
		instrument_type,
		segment,
		instrument_type IN ('CE', 'PE', 'FUT') AS is_fo,
		CASE
			WHEN instrument_type NOT IN ('CE', 'PE', 'FUT') THEN NULL
			WHEN upper(name) = 'NIFTY' THEN 'NSE:NIFTY 50'
			WHEN upper(name) = 'BANKNIFTY' THEN 'NSE:NIFTY BANK'
			WHEN upper(name) = 'MIDCPNIFTY' THEN 'NSE:NIFTY MID SELECT'
			WHEN upper(name) = 'FINNIFTY' THEN 'NSE:NIFTY FIN SERVICE'
			WHEN exchange = 'NFO' THEN 'NSE:' || upper(name)
			WHEN exchange = 'BFO' THEN 'BSE:' || upper(name)
			ELSE exchange || ':' || upper(name)
		END AS underlying_id
	FROM typed_source
),
expiry_dates AS (
	SELECT DISTINCT underlying_id, expiry
	FROM raw
	WHERE underlying_id IS NOT NULL AND expiry IS NOT NULL
),
expiry_numbers AS (
	SELECT
		underlying_id,
		expiry,
		row_number() OVER (
			PARTITION BY underlying_id
			ORDER BY expiry
		) - 1 AS expiry_number
	FROM expiry_dates
),
contract_counts AS (
	SELECT
		underlying_id,
		count(*) FILTER (WHERE instrument_type IN ('CE', 'PE')) AS options_count,
		count(*) FILTER (WHERE instrument_type = 'FUT') AS futures_count
	FROM raw
	WHERE underlying_id IS NOT NULL
	GROUP BY underlying_id
),
enriched AS (
	SELECT
		raw.*,
		expiry_numbers.expiry_number,
		raw.underlying_id IS NOT NULL
			AND EXISTS (
				SELECT 1
				FROM raw AS listed
				WHERE listed.id = raw.underlying_id
			) AS underlying_is_listed,
		coalesce(contract_counts.options_count, 0)::INTEGER AS options_count,
		coalesce(contract_counts.futures_count, 0)::INTEGER AS futures_count,
		CASE
			WHEN raw.instrument_type = 'FUT' THEN concat_ws(' ',
				coalesce(raw.name, raw.trading_symbol),
				strftime(raw.expiry, '%d %b %Y'),
				'Futures'
			)
			WHEN raw.instrument_type IN ('CE', 'PE') THEN concat_ws(' ',
				coalesce(raw.name, raw.trading_symbol),
				strftime(raw.expiry, '%d %b %Y'),
				CASE
					WHEN raw.strike = trunc(raw.strike)
						THEN CAST(CAST(raw.strike AS BIGINT) AS VARCHAR)
					ELSE CAST(raw.strike AS VARCHAR)
				END,
				CASE raw.instrument_type
					WHEN 'CE' THEN 'Call'
					WHEN 'PE' THEN 'Put'
				END
			)
			ELSE raw.trading_symbol
		END AS display_name
	FROM raw
	LEFT JOIN expiry_numbers
		ON raw.underlying_id = expiry_numbers.underlying_id
		AND raw.expiry = expiry_numbers.expiry
	LEFT JOIN contract_counts
		ON coalesce(raw.underlying_id, raw.id) = contract_counts.underlying_id
),
catalog AS (
	SELECT
		*,
		regexp_replace(
			trim(concat_ws(' ',
				display_name,
				exchange,
				CAST(instrument_token AS VARCHAR),
				id,
				trading_symbol,
				instrument_type,
				segment,
				name,
				underlying_id,
				CASE instrument_type
					WHEN 'CE' THEN 'CE CALL'
					WHEN 'PE' THEN 'PE PUT'
					WHEN 'FUT' THEN 'FUT FUTURES'
				END
			)),
			'\s+',
			' ',
			'g'
		) AS search_string
	FROM enriched
)
SELECT
	id,
	exchange,
	trading_symbol,
	instrument_token,
	exchange_token,
	name,
	display_name,
	search_string,
	expiry,
	CAST(expiry_number AS INTEGER) AS expiry_number,
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
FROM catalog;
`
