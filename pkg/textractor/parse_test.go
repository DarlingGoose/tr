package textractor

import (
	"testing"
	"time"
)

func TestSpeakerMergedLinesSameHook(t *testing.T) {
	lines := collectMergedLines(
		&Line{Hook: "Thread A@dialogue.dll:1234", Text: "Alice"},
		&Line{Hook: "Thread A@dialogue.dll:1234", Text: "「Hello」"},
	)

	if len(lines) != 1 {
		t.Fatalf("expected 1 merged line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "Alice" || lines[0].Text != "「Hello」" {
		t.Fatalf("unexpected merged line: %#v", lines[0])
	}
}

func TestSpeakerMergedLinesSameHookJapaneseSpeaker(t *testing.T) {
	lines := collectMergedLines(
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "ボーガン"},
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "「今回のターゲットは、　『ニュー・ソラル』派新進気鋭の才女―」"},
	)

	if len(lines) != 1 {
		t.Fatalf("expected 1 merged line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "ボーガン" || lines[0].Text != "「今回のターゲットは、　『ニュー・ソラル』派新進気鋭の才女―」" {
		t.Fatalf("unexpected merged line: %#v", lines[0])
	}
}

func TestSpeakerMergedLinesSameHookJapaneseGroupSpeaker(t *testing.T) {
	lines := collectMergedLines(
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "部下たち"},
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "「あの女か！　そいつはいっ！」"},
	)

	if len(lines) != 1 {
		t.Fatalf("expected 1 merged line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "部下たち" || lines[0].Text != "「あの女か！　そいつはいっ！」" {
		t.Fatalf("unexpected merged line: %#v", lines[0])
	}
}

func TestSpeakerMergedLinesSeparateHooks(t *testing.T) {
	lines := collectMergedLines(
		&Line{Hook: "Thread A@name.dll:9999", Text: "Alice"},
		&Line{Hook: "Thread B@dialogue.dll:1234", Text: "「Hello」"},
	)

	if len(lines) != 1 {
		t.Fatalf("expected 1 merged line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "Alice" || lines[0].Text != "「Hello」" {
		t.Fatalf("unexpected merged line: %#v", lines[0])
	}
}

func TestSpeakerMergedLinesKeepsSpeakerAcrossUnrelatedHookChatter(t *testing.T) {
	lines := collectLines(FilterLines(SpeakerMergedLines(linesChannel(
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "ボーガン"},
		&Line{Hook: "Thread B@other.dll:1111", Text: "Loading"},
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "「今回のターゲットは、　『ニュー・ソラル』派新進気鋭の才女―」"},
	)), NewHookFilter("@13F548:KSH_dl.exe")))

	if len(lines) != 1 {
		t.Fatalf("expected 1 filtered line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "ボーガン" || lines[0].Text != "「今回のターゲットは、　『ニュー・ソラル』派新進気鋭の才女―」" {
		t.Fatalf("unexpected filtered line: %#v", lines[0])
	}
}

func TestSpeakerMergedLinesIgnoresSymbolJunkSpeaker(t *testing.T) {
	lines := collectMergedLines(
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "\\͎"},
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "「実は、ある人物が我々ネオ・テラーズの秘密を握り、　それを公表しようとしているという情報が入った」"},
	)

	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "" {
		t.Fatalf("symbol junk should not be used as speaker: %#v", lines[0])
	}
}

func TestSpeakerMergedLinesAttachesSpeakerAfterDialogueDuringWait(t *testing.T) {
	lines := collectLines(SpeakerMergedLinesWithOptions(linesChannel(
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "「実は、ある人物が我々ネオ・テラーズの秘密を握り、　それを公表しようとしているという情報が入った」"},
		&Line{Hook: "Thread A@13F548:KSH_dl.exe", Text: "黒幕"},
	), SpeakerMergeOptions{PostDialogueSpeakerWait: time.Second}))

	if len(lines) != 1 {
		t.Fatalf("expected 1 merged line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "黒幕" {
		t.Fatalf("unexpected speaker: %#v", lines[0])
	}
}

func TestSpeakerMergedLinesFlushesUnmatchedSpeakerBeforeNarration(t *testing.T) {
	lines := collectMergedLines(
		&Line{Hook: "Thread A@name.dll:9999", Text: "Alice"},
		&Line{Hook: "Thread A@name.dll:9999", Text: "The room was quiet."},
	)

	if len(lines) != 2 {
		t.Fatalf("expected speaker and narration lines, got %d: %#v", len(lines), lines)
	}
	if lines[0].Text != "Alice" || lines[1].Text != "The room was quiet." {
		t.Fatalf("unexpected lines: %#v", lines)
	}
}

func TestSpeakerMergedLinesFilteredSeesSpeakerBeforeFiltering(t *testing.T) {
	in := make(chan *Line, 2)
	in <- &Line{Hook: "Thread A@name.dll:9999", Text: "Alice"}
	in <- &Line{Hook: "Thread B@dialogue.dll:1234", Text: "「Hello」"}
	close(in)

	lines := collectLines(FilterLines(SpeakerMergedLines(in), NewHookFilter("@dialogue.dll:1234")))
	if len(lines) != 1 {
		t.Fatalf("expected 1 filtered line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "Alice" || lines[0].Text != "「Hello」" {
		t.Fatalf("unexpected filtered line: %#v", lines[0])
	}
}

func collectMergedLines(lines ...*Line) []*Line {
	return collectLines(SpeakerMergedLines(linesChannel(lines...)))
}

func linesChannel(lines ...*Line) <-chan *Line {
	in := make(chan *Line, len(lines))
	for _, line := range lines {
		in <- line
	}
	close(in)
	return in
}

func collectLines(in <-chan *Line) []*Line {
	var lines []*Line
	for line := range in {
		lines = append(lines, line)
	}
	return lines
}
