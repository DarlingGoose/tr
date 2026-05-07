package textractor

import (
	"strings"
	"time"
	"unicode"
)

const (
	speakerLookaheadLines          = 5
	defaultPostDialogueSpeakerWait = 150 * time.Millisecond
)

type SpeakerMergeOptions struct {
	PostDialogueSpeakerWait time.Duration
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
