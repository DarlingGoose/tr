package textractor

import "strings"

func IsNoiseHook(line *Line) bool {
	if line == nil {
		return true
	}

	group := line.HookGroup()

	return strings.Contains(group, "gdi32.dll:") ||
		strings.Contains(group, "GetGlyphOutline") ||
		strings.Contains(group, "GetTextExtent")
}

func FilterLines(in <-chan *Line, filter HookFilter) <-chan *Line {
	out := make(chan *Line)

	go func() {
		defer close(out)

		for line := range in {
			if !filter.Allow(line) {
				continue
			}
			out <- line
		}
	}()

	return out
}
