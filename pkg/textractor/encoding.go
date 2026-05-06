package textractor

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
	"unicode/utf8"
)

func utf16LEBytes(s string) []byte {
	encoded := utf16.Encode([]rune(s))
	out := make([]byte, len(encoded)*2)

	for i, r := range encoded {
		binary.LittleEndian.PutUint16(out[i*2:], r)
	}

	return out
}

func decodeUTF16LEBytes(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xff, 0xfe})

	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}

	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(b[i*2:])
	}

	return string(utf16.Decode(u16))
}

func looksUTF16LE(b []byte) bool {
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		return true
	}

	if len(b) < 4 {
		return false
	}

	var zeros int
	var pairs int

	for i := 1; i < len(b); i += 2 {
		pairs++
		if b[i] == 0 {
			zeros++
		}
	}

	return pairs > 0 && zeros*100/pairs > 40 && !utf8.Valid(b)
}
