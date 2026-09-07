package instruments

import "time"

// InstrumentID is of the form <exchange>:<trading_symbol>
type InstrumentID string

// InstrumentIDMapping is a mapping of an instrument ID to a token and vice versa
type InstrumentIDMapping struct {
	ID    string `json:"id" redis:"id"`
	Token int64  `json:"instrument_token" redis:"instrument_token"`
}

// Instrument contains all metadata for an instrument
type Instrument struct {
	ID              string  `json:"id" redis:"id"`
	InstrumentToken int64   `json:"instrument_token" redis:"instrument_token"`
	ExchangeToken   string  `json:"exchange_token" redis:"exchange_token"`
	TradingSymbol   string  `json:"trading_symbol" redis:"trading_symbol"`
	Exchange        string  `json:"exchange" redis:"exchange"`
	Name            *string `json:"name" redis:"name"`

	Expiry             *time.Time `json:"expiry" redis:"expiry"`
	ExpiryNumber       *int       `json:"expiry_number" redis:"expiry_number"`
	Strike             float64    `json:"strike" redis:"strike"`
	TickSize           float64    `json:"tick_size" redis:"tick_size"`
	LotSize            int        `json:"lot_size" redis:"lot_size"`
	LotUnit            *string    `json:"lot_unit" redis:"lot_unit"`
	InstrumentType     string     `json:"instrument_type" redis:"instrument_type"`
	Segment            string     `json:"segment" redis:"segment"`
	IsFO               bool       `json:"is_fo" redis:"is_fo"`
	UnderlyingID       *string    `json:"underlying_id" redis:"underlying_id"`
	UnderlyingIsListed bool       `json:"underlying_is_listed" redis:"underlying_is_listed"`
	OptionsCount       int        `json:"options_count" redis:"options_count"`
	FuturesCount       int        `json:"futures_count" redis:"futures_count"`

	DisplayName  string `json:"display_name" redis:"display_name"`
	SearchString string `json:"search_string" redis:"search_string"`
}
