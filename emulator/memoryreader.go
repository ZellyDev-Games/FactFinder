package emulator

import "errors"

type ValueType string

const (
	I8        ValueType = "I8"
	I16       ValueType = "I16"
	I32       ValueType = "I32"
	I64       ValueType = "I64"
	U8        ValueType = "U8"
	U16       ValueType = "U16"
	U32       ValueType = "U32"
	U64       ValueType = "U64"
	Bool      ValueType = "Bool"
	FlagCount           = "FlagCount"
)

type ConnectionStatus byte

const (
	Disconnected   ConnectionStatus = 0
	Connected      ConnectionStatus = 1
	Reconnecting   ConnectionStatus = 2
	WaitingForGame ConnectionStatus = 3
)

var ErrGameNotLoaded = errors.New("game not loaded")

type Value struct {
	Type      ValueType
	Name      string
	Signed    int64
	Unsigned  uint64
	Bool      bool
	FlagCount int
}

type MemoryReader interface {
	ConnectEmulator() ConnectionStatus
	EmulatorConnected() ConnectionStatus
	GameConnected() bool
	GetValues(*ReadPlan) ([]Value, error)
}
