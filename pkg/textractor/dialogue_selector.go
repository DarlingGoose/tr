package textractor

import (
	"strings"
	"time"
	"unicode"
)

const (
	defaultDialogueSelectionWindow = 80 * time.Millisecond
	defaultDialogueMinScore        = 25
)

type DialogueSelectorOptions struct {
	// SelectionWindow is the amount of time to wait for competing hooks before
	// choosing the best line. Values <= 0 use the default window.
	SelectionWindow time.Duration

	// MinScore rejects lines that look too noisy or incomplete. Values <= 0 use
	// the default score.
	MinScore int
}

func BestDialogueLines(in <-chan *Line) <-chan *Line {
	return BestDialogueLinesWithOptions(in, DialogueSelectorOptions{})
}

func BestDialogueLinesWithOptions(in <-chan *Line, opts DialogueSelectorOptions) <-chan *Line {
	if opts.SelectionWindow <= 0 {
		opts.SelectionWindow = defaultDialogueSelectionWindow
	}
	if opts.MinScore <= 0 {
		opts.MinScore = defaultDialogueMinScore
	}

	out := make(chan *Line)

	go func() {
		defer close(out)

		var candidates []*Line
		var timer *time.Timer
		var timerC <-chan time.Time
		lastText := ""

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

		startTimer := func() {
			if timer != nil {
				return
			}
			timer = time.NewTimer(opts.SelectionWindow)
			timerC = timer.C
		}

		flush := func() {
			best := bestDialogueCandidate(candidates, opts.MinScore)
			candidates = nil
			stopTimer()
			if best == nil {
				return
			}

			text := normalizeDialogueText(best.Text)
			if isDuplicateDialogueText(lastText, text) {
				return
			}
			lastText = text
			out <- best
		}

		for {
			select {
			case <-timerC:
				flush()
			case line, ok := <-in:
				if !ok {
					flush()
					return
				}
				if scoreDialogueLine(line) < opts.MinScore {
					continue
				}
				candidates = append(candidates, line.Clone())
				startTimer()
			}
		}
	}()

	return out
}

func bestDialogueCandidate(lines []*Line, minScore int) *Line {
	var best *Line
	bestScore := minScore - 1

	for _, line := range lines {
		score := scoreDialogueLine(line)
		if score < minScore {
			continue
		}
		if best == nil || score > bestScore || score == bestScore && lineRuneLen(line) > lineRuneLen(best) {
			best = line
			bestScore = score
		}
	}

	return best
}

func scoreDialogueLine(line *Line) int {
	if line == nil || IsNoiseHook(line) {
		return 0
	}

	text := strings.TrimSpace(line.Text)
	if text == "" || !hasLetterOrNumber(text) {
		return 0
	}

	var letters, numbers, japanese, symbols, controls, replacement int
	for _, r := range text {
		switch {
		case r == unicode.ReplacementChar:
			replacement++
		case unicode.IsControl(r):
			controls++
		case isJapaneseRune(r):
			japanese++
			letters++
		case unicode.IsLetter(r):
			letters++
		case unicode.IsNumber(r):
			numbers++
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			symbols++
		}
	}

	runeLen := len([]rune(text))
	if runeLen == 0 {
		return 0
	}

	score := min(runeLen, 80)
	score += letters * 2
	score += numbers
	score += japanese * 2

	if isDialogueLine(text) {
		score += 30
	}
	if line.Speaker != "" {
		score += 10
	}
	if hasBalancedDialogueQuotes(text) {
		score += 12
	} else if startsDialogueQuote(text) {
		score -= 18
	}

	score -= controls * 25
	score -= replacement * 25

	if symbols*100/runeLen > 35 {
		score -= 35
	}
	if letters+numbers < 2 {
		score -= 25
	}

	return score
}

func lineRuneLen(line *Line) int {
	if line == nil {
		return 0
	}
	return len([]rune(line.Text))
}

func isJapaneseRune(r rune) bool {
	return unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han)
}

func startsDialogueQuote(s string) bool {
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

func hasBalancedDialogueQuotes(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	return strings.HasPrefix(s, "「") && strings.Contains(s[1:], "」") ||
		strings.HasPrefix(s, "『") && strings.Contains(s[1:], "』") ||
		strings.HasPrefix(s, "\"") && strings.Count(s, "\"") >= 2 ||
		strings.HasPrefix(s, "'") && strings.Count(s, "'") >= 2 ||
		strings.HasPrefix(s, "“") && strings.Contains(s[1:], "”")
}

func normalizeDialogueText(s string) string {
	fields := strings.Fields(strings.TrimSpace(s))
	return strings.Join(fields, "")
}

func isDuplicateDialogueText(last, current string) bool {
	if last == "" || current == "" {
		return false
	}
	if last == current {
		return true
	}
	return strings.Contains(last, current)
}
