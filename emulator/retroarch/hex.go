package retroarch

import (
	"FactFinder/emulator"
	"fmt"
)

func isSpace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\r' || b == '\t'
}

func fromHexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// skipField skips one non-whitespace field starting at i, then skips trailing whitespace.
// Returns new index.
func skipField(buf []byte, i int) int {
	// skip leading spaces
	for i < len(buf) && isSpace(buf[i]) {
		i++
	}
	// skip non-space
	for i < len(buf) && !isSpace(buf[i]) {
		i++
	}
	// skip trailing spaces
	for i < len(buf) && isSpace(buf[i]) {
		i++
	}
	return i
}

// parseHexByteToken expects a 2-hex-digit token at buf[i:], returns byte value and new index.
// It tolerates tokens longer than 2 by consuming until whitespace after reading first 2 hex digits.
func parseHexByteToken(buf []byte, i int) (byte, int, error) {
	// skip leading spaces
	for i < len(buf) && isSpace(buf[i]) {
		i++
	}
	if i+2 > len(buf) {
		return 0, i, fmt.Errorf("truncated hex token")
	}

	hi, ok := fromHexNibble(buf[i])
	if !ok {
		return 0, i, fmt.Errorf("invalid hex char %q", buf[i])
	}
	lo, ok := fromHexNibble(buf[i+1])
	if !ok {
		return 0, i, fmt.Errorf("invalid hex char %q", buf[i+1])
	}
	v := (hi << 4) | lo
	i += 2

	// consume remainder of token (defensive), then whitespace
	for i < len(buf) && !isSpace(buf[i]) {
		i++
	}
	for i < len(buf) && isSpace(buf[i]) {
		i++
	}

	return v, i, nil
}

// decodeRetroArchReadCoreMemoryBytes expects a response like:
// "READ_CORE_MEMORY <addr> <b0> <b1> ..."
// It skips the first 2 fields and decodes `want` hex byte tokens into dst.
// dst must have length >= want.
func decodeRetroArchReadCoreMemoryBytes(resp []byte, dst []byte, want int) error {
	// Skip "READ_CORE_MEMORY"
	i := 0
	i = skipField(resp, i)
	// Skip "<addr>"
	i = skipField(resp, i)

	if i < len(resp) && resp[i] == '-' {
		return emulator.GameNotLoadedError
	}

	for j := 0; j < want; j++ {
		b, ni, err := parseHexByteToken(resp, i)
		if err != nil {
			return err
		}
		dst[j] = b
		i = ni
	}

	return nil
}

func appendHexUpper(dst []byte, v uint64) []byte {
	// Write v in uppercase hex with no 0x prefix.
	// This avoids strconv.FormatUint allocations and avoids lowercase a-f.
	if v == 0 {
		return append(dst, '0')
	}

	// Max 16 hex digits for uint64
	var tmp [16]byte
	i := len(tmp)

	for v != 0 {
		n := v & 0xF
		i--
		if n < 10 {
			tmp[i] = byte('0' + n)
		} else {
			tmp[i] = byte('A' + (n - 10))
		}
		v >>= 4
	}

	return append(dst, tmp[i:]...)
}
