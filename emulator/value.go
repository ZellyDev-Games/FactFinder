package emulator

import (
	"encoding/binary"
	"math/bits"
)

// GB/GBC/GBA/SNES/NES/DS/3DS/PSX = little endian
// Genesis/N64 = big endian
func DecodeValue(readSpec ReadSpec, raw []byte) *Value {
	val := Value{
		Type: readSpec.Type,
		Name: readSpec.Name,
	}

	need := readSpec.Size()

	var u uint64

	switch need {
	case 1:
		u = uint64(raw[0])

	case 2:
		u = uint64(binary.LittleEndian.Uint16(raw))

	case 4:
		u = uint64(binary.LittleEndian.Uint32(raw))

	case 8:
		u = binary.LittleEndian.Uint64(raw)

	default:
		return nil
	}

	switch readSpec.Type {

	case I8:
		val.Signed = int64(int8(raw[0]))

	case I16:
		val.Signed = int64(int16(binary.LittleEndian.Uint16(raw)))

	case I32:
		val.Signed = int64(int32(binary.LittleEndian.Uint32(raw)))

	case I64:
		val.Signed = int64(binary.LittleEndian.Uint64(raw))

	case U8, U16, U32, U64:
		val.Unsigned = u

	case Bool:
		val.Bool = u != 0

	case FlagCount:
		if readSpec.Mask != 0 {
			u &= 0x3FFF
		}

		val.FlagCount = bits.OnesCount64(u)
	}

	return &val
}
