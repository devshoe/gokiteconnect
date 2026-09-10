package instruments

// TokenIndex is an immutable, concurrency-safe snapshot of ID/token mappings.
type TokenIndex struct {
	tokensByID map[InstrumentID]int64
	idsByToken map[int64]InstrumentID
}

func newTokenIndex(instruments []Instrument) *TokenIndex {
	index := &TokenIndex{
		tokensByID: make(map[InstrumentID]int64, len(instruments)),
		idsByToken: make(map[int64]InstrumentID, len(instruments)),
	}
	for _, instrument := range instruments {
		index.tokensByID[instrument.ID] = instrument.InstrumentToken
		index.idsByToken[instrument.InstrumentToken] = instrument.ID
	}
	return index
}

// Token returns the token mapped to id.
func (i *TokenIndex) Token(id InstrumentID) (int64, bool) {
	if i == nil {
		return 0, false
	}
	normalizedID, err := ParseInstrumentID(string(id))
	if err != nil {
		return 0, false
	}
	token, ok := i.tokensByID[normalizedID]
	return token, ok
}

// ID returns the instrument ID mapped to token.
func (i *TokenIndex) ID(token int64) (InstrumentID, bool) {
	if i == nil {
		return "", false
	}
	id, ok := i.idsByToken[token]
	return id, ok
}

// Len returns the number of mappings in the snapshot.
func (i *TokenIndex) Len() int {
	if i == nil {
		return 0
	}
	return len(i.tokensByID)
}
