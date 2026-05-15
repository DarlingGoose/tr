package textractor

import (
	"testing"
	"time"
)

func TestBestDialogueLinesPrefersReadableDialogueOverJunkHook(t *testing.T) {
	lines := collectLines(BestDialogueLinesWithOptions(linesChannel(
		&Line{Hook: "Thread A@bad.dll:1111", Text: "0,gS_k& &"},
		&Line{Hook: "Thread B@dialogue.dll:1234", Text: "「今回のターゲットはニュー・ソラルだ」"},
	), DialogueSelectorOptions{SelectionWindow: time.Hour}))

	if len(lines) != 1 {
		t.Fatalf("expected 1 selected line, got %d: %#v", len(lines), lines)
	}
	if lines[0].HookGroup() != "@dialogue.dll:1234" {
		t.Fatalf("selected wrong hook: %#v", lines[0])
	}
	if lines[0].Text != "「今回のターゲットはニュー・ソラルだ」" {
		t.Fatalf("selected wrong text: %#v", lines[0])
	}
}

func TestBestDialogueLinesPrefersCompleteDialogueOverPartialHook(t *testing.T) {
	lines := collectLines(BestDialogueLinesWithOptions(linesChannel(
		&Line{Hook: "Thread A@partial.dll:1111", Text: "「今回のターゲットは"},
		&Line{Hook: "Thread B@dialogue.dll:1234", Text: "「今回のターゲットはニュー・ソラルだ」"},
	), DialogueSelectorOptions{SelectionWindow: time.Hour}))

	if len(lines) != 1 {
		t.Fatalf("expected 1 selected line, got %d: %#v", len(lines), lines)
	}
	if lines[0].HookGroup() != "@dialogue.dll:1234" {
		t.Fatalf("selected wrong hook: %#v", lines[0])
	}
	if lines[0].Text != "「今回のターゲットはニュー・ソラルだ」" {
		t.Fatalf("selected wrong text: %#v", lines[0])
	}
}

func TestBestDialogueLinesSuppressesContainedDuplicateAfterFullLine(t *testing.T) {
	lines := collectLines(BestDialogueLinesWithOptions(linesChannel(
		&Line{Hook: "Thread A@dialogue.dll:1234", Text: "「秘密を握っている人物がいる」"},
		&Line{Hook: "Thread B@partial.dll:1111", Text: "秘密を握っている"},
	), DialogueSelectorOptions{SelectionWindow: time.Hour}))

	if len(lines) != 1 {
		t.Fatalf("expected duplicate to be suppressed, got %d lines: %#v", len(lines), lines)
	}
	if lines[0].Text != "「秘密を握っている人物がいる」" {
		t.Fatalf("unexpected selected line: %#v", lines[0])
	}
}

func TestBestDialogueLinesKeepsSpeakerMergedAcrossHooks(t *testing.T) {
	lines := collectLines(BestDialogueLinesWithOptions(SpeakerMergedLinesWithOptions(linesChannel(
		&Line{Hook: "Thread A@name.dll:9999", Text: "ボーガン"},
		&Line{Hook: "Thread B@dialogue.dll:1234", Text: "「今回のターゲットはニュー・ソラルだ」"},
	), SpeakerMergeOptions{PostDialogueSpeakerWait: 0}), DialogueSelectorOptions{SelectionWindow: time.Hour}))

	if len(lines) != 1 {
		t.Fatalf("expected 1 selected line, got %d: %#v", len(lines), lines)
	}
	if lines[0].Speaker != "ボーガン" {
		t.Fatalf("expected merged speaker, got %#v", lines[0])
	}
}
