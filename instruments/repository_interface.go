package instruments

type InstrumentRepository interface {
	GetInstrument()
	GetFutures(underlying string)
	GetOptions(underlying string)
}

type InstrumentClient struct {
	repository     InstrumentRepository
	persistentPath string
}
