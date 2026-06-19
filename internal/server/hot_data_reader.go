package server

import "quack/internal/hotdata"

type HotDataReader = hotdata.HotDataReader
type HotDataInvalidator = hotdata.HotDataInvalidator
type MutableHotDataReader = hotdata.MutableHotDataReader

func NewPassthroughHotDataReader(db hotdata.Source) HotDataReader {
	return hotdata.NewPassthroughHotDataReader(db)
}

func NewMemoryHotDataReader(source HotDataReader, opts MemoryHotDataReaderOptions) MutableHotDataReader {
	return hotdata.NewMemoryHotDataReader(source, opts)
}

func NewOtterHotDataReader(source HotDataReader, opts OtterHotDataReaderOptions) MutableHotDataReader {
	return hotdata.NewOtterHotDataReader(source, opts)
}
