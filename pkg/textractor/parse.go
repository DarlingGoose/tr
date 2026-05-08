package textractor

import (
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var textractorLineRE = regexp.MustCompile(`^(?:TEXT:\s*)?\[([^\]]+)\]\s*(.*)$`)

type Line struct {
	Raw     string
	Hook    string
	Text    string
	Speaker string
}

func (l *Line) HookGroup() string {
	if l == nil {
		return ""
	}

	return HookGroup(l.Hook)
}

func (l *Line) Clone() *Line {
	if l == nil {
		return nil
	}

	cp := *l
	return &cp
}

func HookGroup(hook string) string {
	hook = strings.TrimSpace(hook)
	if hook == "" {
		return ""
	}

	if idx := strings.LastIndexByte(hook, '@'); idx >= 0 {
		return hook[idx:]
	}

	return hook
}

func ParseTextractorLine(raw string) (Line, error) {
	line := CleanTextractorLine(raw)

	m := textractorLineRE.FindStringSubmatch(line)
	if m == nil {
		return Line{
			Raw: raw,
		}, nil
	}

	text := CollapseAdjacentDuplicateRunes(strings.TrimSpace(m[2]))
	return Line{
		Hook: strings.TrimSpace(m[1]),
		Text: text,
		Raw:  raw,
	}, nil
}

func CleanTextractorLine(raw string) string {
	b := []byte(raw)

	// Strip common BOM / leading NUL junk.
	for len(b) > 0 && (b[0] == 0x00 || b[0] == 0xEF || b[0] == 0xBB || b[0] == 0xBF) {
		b = b[1:]
	}

	// If it looks like UTF-16LE, decode it properly.
	if looksUTF16LE(b) {
		return strings.TrimSpace(decodeUTF16LE(b))
	}

	// Sometimes only one leading NUL sneaks in before valid UTF-8.
	s := strings.TrimLeft(string(b), "\x00")
	s = strings.ReplaceAll(s, "\x00", "")

	return strings.TrimSpace(s)
}

func decodeUTF16LE(b []byte) string {
	// Drop odd trailing byte.
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}

	u16 := make([]uint16, 0, len(b)/2)

	for i := 0; i+1 < len(b); i += 2 {
		v := uint16(b[i]) | uint16(b[i+1])<<8

		// Skip NUL chars.
		if v == 0 {
			continue
		}

		u16 = append(u16, v)
	}

	s := string(utf16.Decode(u16))

	// Cleanup invalid UTF-8 replacement noise just in case.
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}

	return strings.TrimSpace(s)
}

func CollapseAdjacentDuplicateRunes(s string) string {
	runes := []rune(s)
	if len(runes) < 2 {
		return s
	}

	out := make([]rune, 0, len(runes))

	for i := 0; i < len(runes); i++ {
		out = append(out, runes[i])

		// If the next rune is identical, skip it.
		if i+1 < len(runes) && runes[i] == runes[i+1] {
			i++
		}
	}

	return string(out)
}
