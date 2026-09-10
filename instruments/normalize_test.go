package instruments

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	kiteconnect "github.com/devshoe/gokiteconnect"
	"github.com/devshoe/gokiteconnect/models"
)

func TestParseInstrumentID(t *testing.T) {
	id, err := ParseInstrumentID(" nse:nifty 50 ")
	if err != nil {
		t.Fatalf("ParseInstrumentID() error = %v", err)
	}
	if id != "NSE:NIFTY 50" {
		t.Fatalf("ParseInstrumentID() = %q, want NSE:NIFTY 50", id)
	}

	for _, value := range []string{"", "NSE", ":INFY", "NSE:", "NSE:INFY:EQ"} {
		if _, err := ParseInstrumentID(value); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ParseInstrumentID(%q) error = %v, want ErrInvalidInput", value, err)
		}
	}
}

func TestNormalizeSnapshotDerivesCatalogMetadata(t *testing.T) {
	nearExpiry := instrumentDate(2026, time.June, 25)
	farExpiry := instrumentDate(2026, time.July, 30)
	source := kiteconnect.Instruments{
		rawInstrument(256265, "NSE", "NIFTY 50", "NIFTY 50", "INDICES", "EQ", time.Time{}, 0),
		rawInstrument(408065, "NSE", "INFY", "INFOSYS", "NSE", "EQ", time.Time{}, 0),
		rawInstrument(1004, "BSE", "SENSEX", "SENSEX", "INDICES", "EQ", time.Time{}, 0),
		rawInstrument(2002, "NFO", "NIFTY26JULFUT", "NIFTY", "NFO-FUT", "FUT", farExpiry, 0),
		rawInstrument(2001, "NFO", "NIFTY26JUNFUT", "NIFTY", "NFO-FUT", "FUT", nearExpiry, 0),
		rawInstrument(3001, "NFO", "NIFTY26JUN20000CE", "NIFTY", "NFO-OPT", "CE", nearExpiry, 20000),
		rawInstrument(3002, "NFO", "NIFTY26JUN20000PE", "NIFTY", "NFO-OPT", "PE", nearExpiry, 20000),
		rawInstrument(4001, "BFO", "SENSEX26JUN80000CE", "SENSEX", "BFO-OPT", "CE", nearExpiry, 80000),
		rawInstrument(5001, "MCX", "CRUDEOIL26JUNFUT", "CRUDEOIL", "MCX-FUT", "FUT", nearExpiry, 0),
	}

	got, err := normalizeSnapshot(source)
	if err != nil {
		t.Fatalf("normalizeSnapshot() error = %v", err)
	}
	if len(got) != len(source) {
		t.Fatalf("normalizeSnapshot() returned %d rows, want %d", len(got), len(source))
	}

	byID := make(map[InstrumentID]Instrument, len(got))
	for _, instrument := range got {
		byID[instrument.ID] = instrument
	}

	index := byID["NSE:NIFTY 50"]
	if index.InstrumentType != "INDICES" || index.OptionsCount != 2 || index.FuturesCount != 2 {
		t.Fatalf("normalized index = %#v, want INDICES with 2 options and 2 futures", index)
	}

	nearFuture := byID["NFO:NIFTY26JUNFUT"]
	farFuture := byID["NFO:NIFTY26JULFUT"]
	if nearFuture.UnderlyingID == nil || *nearFuture.UnderlyingID != "NSE:NIFTY 50" || !nearFuture.UnderlyingIsListed {
		t.Fatalf("near future underlying = %#v, want listed NSE:NIFTY 50", nearFuture)
	}
	if nearFuture.ExpiryNumber == nil || *nearFuture.ExpiryNumber != 0 {
		t.Fatalf("near future expiry number = %v, want 0", nearFuture.ExpiryNumber)
	}
	if farFuture.ExpiryNumber == nil || *farFuture.ExpiryNumber != 1 {
		t.Fatalf("far future expiry number = %v, want 1", farFuture.ExpiryNumber)
	}

	call := byID["NFO:NIFTY26JUN20000CE"]
	if call.DisplayName != "NIFTY 25 Jun 2026 20000 Call" {
		t.Fatalf("call display name = %q", call.DisplayName)
	}
	for _, term := range []string{"CALL", "CE", "NSE:NIFTY 50", "3001"} {
		if !strings.Contains(call.SearchString, term) {
			t.Errorf("call search string %q does not contain %q", call.SearchString, term)
		}
	}

	bfo := byID["BFO:SENSEX26JUN80000CE"]
	if bfo.UnderlyingID == nil || *bfo.UnderlyingID != "BSE:SENSEX" || !bfo.UnderlyingIsListed {
		t.Fatalf("BFO underlying = %#v, want listed BSE:SENSEX", bfo.UnderlyingID)
	}
	mcx := byID["MCX:CRUDEOIL26JUNFUT"]
	if mcx.UnderlyingID == nil || *mcx.UnderlyingID != "MCX:CRUDEOIL" || mcx.UnderlyingIsListed {
		t.Fatalf("MCX underlying = %#v listed=%v", mcx.UnderlyingID, mcx.UnderlyingIsListed)
	}
	if byID["NSE:INFY"].Expiry != nil || byID["NSE:INFY"].Name == nil {
		t.Fatalf("cash instrument nullable fields = %#v", byID["NSE:INFY"])
	}
}

func TestNormalizeSnapshotRejectsInvalidSnapshots(t *testing.T) {
	if _, err := normalizeSnapshot(nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty snapshot error = %v, want ErrInvalidInput", err)
	}

	valid := rawInstrument(1, "NSE", "INFY", "INFOSYS", "NSE", "EQ", time.Time{}, 0)
	tests := map[string]kiteconnect.Instruments{
		"duplicate ID":    {valid, rawInstrument(2, "NSE", "INFY", "INFOSYS", "NSE", "EQ", time.Time{}, 0)},
		"duplicate token": {valid, rawInstrument(1, "NSE", "TCS", "TCS", "NSE", "EQ", time.Time{}, 0)},
		"fractional lot":  {instrumentWithLotSize(valid, 1.5)},
		"missing expiry":  {rawInstrument(3, "NFO", "INFY26JUNFUT", "INFY", "NFO-FUT", "FUT", time.Time{}, 0)},
		"missing name":    {rawInstrument(4, "NFO", "INFY26JUNFUT", "", "NFO-FUT", "FUT", instrumentDate(2026, time.June, 25), 0)},
	}
	for name, snapshot := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeSnapshot(snapshot); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("normalizeSnapshot() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeFilters(t *testing.T) {
	minStrike, maxStrike := 100.0, 200.0
	got, err := normalizeOptionsFilter(OptionsFilter{
		UnderlyingID:  "nse:infy",
		ExpiryNumbers: []int{1, 0, 1},
		ExpiryDates:   []time.Time{instrumentDate(2026, time.July, 30), instrumentDate(2026, time.June, 25)},
		Strikes:       &StrikeRange{Min: &minStrike, Max: &maxStrike},
		Types:         []OptionType{"pe", "CE", "CE"},
	})
	if err != nil {
		t.Fatalf("normalizeOptionsFilter() error = %v", err)
	}
	if got.UnderlyingID != "NSE:INFY" || !reflect.DeepEqual(got.ExpiryNumbers, []int{0, 1}) || !reflect.DeepEqual(got.Types, []OptionType{OptionTypeCall, OptionTypePut}) {
		t.Fatalf("normalized filter = %#v", got)
	}

	negative := -1.0
	for name, filter := range map[string]OptionsFilter{
		"missing underlying": {},
		"negative expiry":    {UnderlyingID: "NSE:INFY", ExpiryNumbers: []int{-1}},
		"unknown type":       {UnderlyingID: "NSE:INFY", Types: []OptionType{"XX"}},
		"negative strike":    {UnderlyingID: "NSE:INFY", Strikes: &StrikeRange{Min: &negative}},
		"reversed strikes":   {UnderlyingID: "NSE:INFY", Strikes: &StrikeRange{Min: &maxStrike, Max: &minStrike}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeOptionsFilter(filter); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("normalizeOptionsFilter() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func rawInstrument(token int, exchange, symbol, name, segment, instrumentType string, expiry time.Time, strike float64) kiteconnect.Instrument {
	return kiteconnect.Instrument{
		InstrumentToken: token,
		ExchangeToken:   token + 10,
		Tradingsymbol:   symbol,
		Name:            name,
		Expiry:          models.Time{Time: expiry},
		StrikePrice:     strike,
		TickSize:        0.05,
		LotSize:         50,
		InstrumentType:  instrumentType,
		Segment:         segment,
		Exchange:        exchange,
	}
}

func instrumentWithLotSize(instrument kiteconnect.Instrument, lotSize float64) kiteconnect.Instrument {
	instrument.LotSize = lotSize
	return instrument
}

func instrumentDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, indiaLocation)
}
