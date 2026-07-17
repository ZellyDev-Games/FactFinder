package emulator

import "errors"

type ValueType string

const (
	F32       ValueType = "F32"
	F64       ValueType = "F64"
	String    ValueType = "String"
	UTF16LE   ValueType = "UTF16LE"
	I8        ValueType = "I8"
	I16       ValueType = "I16"
	I32       ValueType = "I32"
	I64       ValueType = "I64"
	U8        ValueType = "U8"
	U16       ValueType = "U16"
	U32       ValueType = "U32"
	U64       ValueType = "U64"
	Bool      ValueType = "Bool"
	FlagCount ValueType = "FlagCount"
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
	Float32   float32
	Float64   float64
	String    string
	Bool      bool
	FlagCount int
}

type Connector interface {
	ConnectEmulator() ConnectionStatus
	EmulatorConnected() ConnectionStatus
	GameConnected() bool
	Close() error
}

type Reader interface {
	GetValues(*CompiledReadPlan) ([]Value, error)
}

type Planner interface {
	CompileReadPlan(plan *ReadPlan) *CompiledReadPlan
}

type MemoryReader interface {
	Connector
	Reader
	Planner
}
