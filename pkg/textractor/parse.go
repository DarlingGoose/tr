package textractor

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var textractorLineRE = regexp.MustCompile(`^(?:TEXT:\s*)?\[([^\]]+)\]\s*(.*)$`)

const (
	speakerLookaheadLines          = 5
	defaultPostDialogueSpeakerWait = 150 * time.Millisecond
)

type Line struct {
	Hook    string
	Text    string
	Speaker string
}

type SpeakerMergeOptions struct {
	PostDialogueSpeakerWait time.Duration
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
		return Line{}, errors.New("line does not match textractor format: " + line)
	}

	text := CollapseAdjacentDuplicateRunes(strings.TrimSpace(m[2]))
	return Line{
		Hook: strings.TrimSpace(m[1]),
		Text: text,
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

func isDialogueLine(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	switch []rune(s)[0] {
	case '「', '『', '"', '\'', '“':
		return true
	default:
		return false
	}
}

func isLikelySpeakerLine(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	r := []rune(s)

	// Avoid treating full dialogue/narration as a speaker.
	if len(r) > 24 {
		return false
	}

	// Speaker names usually do not include sentence punctuation.
	if strings.ContainsAny(s, "。！？!?「」『』…") {
		return false
	}

	// Avoid obvious narration fragments.
	if strings.ContainsAny(s, "、,.") {
		return false
	}

	if !hasLetterOrNumber(s) {
		return false
	}

	return true
}

func hasLetterOrNumber(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func SpeakerMergedLines(in <-chan *Line) <-chan *Line {
	return SpeakerMergedLinesWithOptions(in, SpeakerMergeOptions{
		PostDialogueSpeakerWait: defaultPostDialogueSpeakerWait,
	})
}

func SpeakerMergedLinesWithOptions(in <-chan *Line, opts SpeakerMergeOptions) <-chan *Line {
	out := make(chan *Line)

	go func() {
		defer close(out)

		type pendingSpeakerLine struct {
			line *Line
			seq  int
		}

		pendingSpeaker := make(map[string]pendingSpeakerLine)
		pendingDialogue := make(map[string]*Line)
		var lastSpeaker pendingSpeakerLine
		var hasLastSpeaker bool
		var lastDialogue *Line
		seq := 0
		var timer *time.Timer
		var timerC <-chan time.Time

		stopTimer := func() {
			if timer == nil {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
			timerC = nil
		}

		resetTimer := func() {
			if opts.PostDialogueSpeakerWait <= 0 || len(pendingDialogue) == 0 {
				stopTimer()
				return
			}
			if timer == nil {
				timer = time.NewTimer(opts.PostDialogueSpeakerWait)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(opts.PostDialogueSpeakerWait)
			}
			timerC = timer.C
		}

		flushPendingDialogue := func(group string) {
			line := pendingDialogue[group]
			if line == nil {
				return
			}
			out <- line
			delete(pendingDialogue, group)
			if lastDialogue == line {
				lastDialogue = nil
			}
			resetTimer()
		}

		flushAllPendingDialogue := func() {
			for group := range pendingDialogue {
				flushPendingDialogue(group)
			}
			stopTimer()
		}

		for {
			select {
			case <-timerC:
				flushAllPendingDialogue()
				continue
			case line, ok := <-in:
				if !ok {
					flushAllPendingDialogue()
					for _, speaker := range pendingSpeaker {
						if speaker.line != nil {
							out <- speaker.line
						}
					}
					return
				}

				if line == nil || IsNoiseHook(line) {
					continue
				}

				line.Text = strings.TrimSpace(line.Text)
				if line.Text == "" {
					continue
				}
				if !hasLetterOrNumber(line.Text) {
					continue
				}

				seq++
				group := line.HookGroup()

				// Prefer a speaker from the same hook, but fall back to the most
				// recent speaker line because many games emit names and dialogue on
				// separate hooks.
				if isDialogueLine(line.Text) {
					if speaker, ok := pendingSpeaker[group]; ok && speaker.line != nil {
						line.Speaker = speaker.line.Text
						delete(pendingSpeaker, group)
						if hasLastSpeaker && lastSpeaker.line == speaker.line {
							hasLastSpeaker = false
						}
					} else if hasLastSpeaker && seq-lastSpeaker.seq <= speakerLookaheadLines {
						line.Speaker = lastSpeaker.line.Text
						delete(pendingSpeaker, lastSpeaker.line.HookGroup())
						hasLastSpeaker = false
					}

					if line.Speaker == "" && opts.PostDialogueSpeakerWait > 0 {
						if old := pendingDialogue[group]; old != nil {
							out <- old
						}
						pendingDialogue[group] = line
						lastDialogue = line
						resetTimer()
						continue
					}

					out <- line
					continue
				}

				if isLikelySpeakerLine(line.Text) {
					if dialogue := pendingDialogue[group]; dialogue != nil {
						dialogue.Speaker = line.Text
						out <- dialogue
						delete(pendingDialogue, group)
						if lastDialogue == dialogue {
							lastDialogue = nil
						}
						resetTimer()
						continue
					}
					if lastDialogue != nil {
						lastDialogue.Speaker = line.Text
						out <- lastDialogue
						delete(pendingDialogue, lastDialogue.HookGroup())
						lastDialogue = nil
						resetTimer()
						continue
					}

					// If a previous speaker for this same group was never consumed,
					// emit it before replacing it.
					if old, ok := pendingSpeaker[group]; ok && old.line != nil {
						out <- old.line
						if hasLastSpeaker && lastSpeaker.line == old.line {
							hasLastSpeaker = false
							continue //idk maybe
						}
					}

					lastSpeaker = pendingSpeakerLine{line: line, seq: seq}
					hasLastSpeaker = true
					pendingSpeaker[group] = lastSpeaker
					continue
				}

				// Normal narration/non-dialogue line.
				// If a pending speaker exists for this same group, it was probably
				// not actually a speaker, so flush it first.
				if old, ok := pendingSpeaker[group]; ok && old.line != nil {
					out <- old.line
					delete(pendingSpeaker, group)
					if hasLastSpeaker && lastSpeaker.line == old.line {
						hasLastSpeaker = false
					}
				} else if hasLastSpeaker && seq-lastSpeaker.seq > speakerLookaheadLines {
					out <- lastSpeaker.line
					delete(pendingSpeaker, lastSpeaker.line.HookGroup())
					hasLastSpeaker = false
				}

				for group, speaker := range pendingSpeaker {
					if speaker.line == nil || seq-speaker.seq <= speakerLookaheadLines {
						continue
					}
					out <- speaker.line
					delete(pendingSpeaker, group)
				}

				out <- line
			}
		}
	}()

	return out
}

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
