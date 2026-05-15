package textractor

import (
	"io"
	"testing"
)

type chunkedStringReader struct {
	s     string
	sizes []int
}

func (r *chunkedStringReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}

	n := len(r.s)
	if len(r.sizes) > 0 {
		n = r.sizes[0]
		r.sizes = r.sizes[1:]
	}
	if n > len(r.s) {
		n = len(r.s)
	}
	if n > len(p) {
		n = len(p)
	}

	copy(p, r.s[:n])
	r.s = r.s[n:]
	return n, nil
}

func TestReadOutputPreservesSplitUTF8Line(t *testing.T) {
	client := newTestClient(t, 10)
	raw := "[Thread A@dialogue.dll:1234] first\n[Thread B@names.dll:9999] second\n"

	client.readOutput(&chunkedStringReader{
		s:     raw,
		sizes: []int{12, 9, 5, 8},
	})

	lines := collectRawLines(client.RawLines())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %#v", len(lines), lines)
	}
	if lines[0].HookGroup() != "@dialogue.dll:1234" || lines[0].Text != "first" {
		t.Fatalf("unexpected first line: %#v", lines[0])
	}
	if lines[1].HookGroup() != "@names.dll:9999" || lines[1].Text != "second" {
		t.Fatalf("unexpected second line: %#v", lines[1])
	}
}

func TestReadOutputPreservesSplitUTF16Line(t *testing.T) {
	client := newTestClient(t, 10)
	raw := utf16LEBytes("[Thread A@dialogue.dll:1234] first\n[Thread B@names.dll:9999] second\n")

	client.readOutput(&chunkedByteReader{
		b:     raw,
		sizes: []int{11, 7, 13, 5},
	})

	lines := collectRawLines(client.RawLines())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %#v", len(lines), lines)
	}
	if lines[0].HookGroup() != "@dialogue.dll:1234" || lines[0].Text != "first" {
		t.Fatalf("unexpected first line: %#v", lines[0])
	}
	if lines[1].HookGroup() != "@names.dll:9999" || lines[1].Text != "second" {
		t.Fatalf("unexpected second line: %#v", lines[1])
	}
}

func TestReadOutputPreservesUTF16LineWhenChunkEndsAfterNewlineByte(t *testing.T) {
	client := newTestClient(t, 10)
	first := "[Thread A@dialogue.dll:1234] first\n"
	raw := utf16LEBytes(first + "[Thread B@names.dll:9999] second\n")

	client.readOutput(&chunkedByteReader{
		b:     raw,
		sizes: []int{len(utf16LEBytes(first)) - 1, 1},
	})

	lines := collectRawLines(client.RawLines())
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %#v", len(lines), lines)
	}
	if lines[0].HookGroup() != "@dialogue.dll:1234" || lines[0].Text != "first" {
		t.Fatalf("unexpected first line: %#v", lines[0])
	}
	if lines[1].HookGroup() != "@names.dll:9999" || lines[1].Text != "second" {
		t.Fatalf("unexpected second line: %#v", lines[1])
	}
}

type chunkedByteReader struct {
	b     []byte
	sizes []int
}

func (r *chunkedByteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}

	n := len(r.b)
	if len(r.sizes) > 0 {
		n = r.sizes[0]
		r.sizes = r.sizes[1:]
	}
	if n > len(r.b) {
		n = len(r.b)
	}
	if n > len(p) {
		n = len(p)
	}

	copy(p, r.b[:n])
	r.b = r.b[n:]
	return n, nil
}

func collectRawLines(lines <-chan *Line) []*Line {
	var out []*Line
	for line := range lines {
		out = append(out, line)
	}
	return out
}
