package textractor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newTestClient(t *testing.T, historyLimit int) *Client {
	t.Helper()

	client, err := NewClient(ClientOptions{
		WinePrefix:       t.TempDir(),
		HookHistoryLimit: historyLimit,
	})
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func TestHookHistoryStoresLinesByGroup(t *testing.T) {
	client := newTestClient(t, 10)

	client.recordHookLine(&Line{Hook: "Thread A@dialogue.dll:1234", Text: "first"})
	client.recordHookLine(&Line{Hook: "Thread B@names.dll:9999", Text: "name"})
	client.recordHookLine(&Line{Hook: "Thread C@dialogue.dll:1234", Text: "second"})

	groups := client.HookGroups()
	if len(groups) != 2 || groups[0] != "@dialogue.dll:1234" || groups[1] != "@names.dll:9999" {
		t.Fatalf("unexpected groups: %#v", groups)
	}

	history := client.HookHistory("@dialogue.dll:1234")
	if len(history) != 2 {
		t.Fatalf("expected 2 dialogue lines, got %d", len(history))
	}
	if history[0].Text != "first" || history[1].Text != "second" {
		t.Fatalf("unexpected history: %#v", history)
	}

	history[0].Text = "mutated"
	if got := client.HookHistory("@dialogue.dll:1234")[0].Text; got != "first" {
		t.Fatalf("history was not copied: %q", got)
	}
}

func TestHookHistoryLimit(t *testing.T) {
	client := newTestClient(t, 2)

	client.recordHookLine(&Line{Hook: "Thread A@dialogue.dll:1234", Text: "one"})
	client.recordHookLine(&Line{Hook: "Thread A@dialogue.dll:1234", Text: "two"})
	client.recordHookLine(&Line{Hook: "Thread A@dialogue.dll:1234", Text: "three"})

	history := client.HookHistory("@dialogue.dll:1234")
	if len(history) != 2 {
		t.Fatalf("expected 2 retained lines, got %d", len(history))
	}
	if history[0].Text != "two" || history[1].Text != "three" {
		t.Fatalf("unexpected retained history: %#v", history)
	}
}

func TestHookFeedReplaysAndStreamsSelectedGroup(t *testing.T) {
	client := newTestClient(t, 10)

	client.recordHookLine(&Line{Hook: "Thread A@dialogue.dll:1234", Text: "before"})
	client.recordHookLine(&Line{Hook: "Thread B@names.dll:9999", Text: "ignored"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	feed := client.HookFeed(ctx, "@dialogue.dll:1234", true)
	if got := readFeedLine(t, feed).Text; got != "before" {
		t.Fatalf("unexpected replay line: %q", got)
	}

	client.recordHookLine(&Line{Hook: "Thread B@names.dll:9999", Text: "still ignored"})
	client.recordHookLine(&Line{Hook: "Thread A@dialogue.dll:1234", Text: "after"})

	if got := readFeedLine(t, feed).Text; got != "after" {
		t.Fatalf("unexpected live line: %q", got)
	}

	cancel()
	if _, ok := <-feed; ok {
		t.Fatal("expected feed to close after cancel")
	}
}

func TestHookFeedReplaysAllHistoryBeyondDefaultBuffer(t *testing.T) {
	client := newTestClient(t, 1)

	const want = 300
	for i := range want {
		client.recordHookLine(&Line{
			Hook: "Thread A@hook-" + strconv.Itoa(i),
			Text: "line",
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	feed := client.HookFeed(ctx, "", true)
	seen := make(map[string]struct{}, want)
	for range want {
		line := readFeedLine(t, feed)
		seen[line.HookGroup()] = struct{}{}
	}

	if len(seen) != want {
		t.Fatalf("expected %d replayed hook groups, got %d", want, len(seen))
	}
}

func TestHookHistoryLogWritesSelectedGroupsUnderGameLogs(t *testing.T) {
	gameDir := t.TempDir()
	client, err := NewClient(ClientOptions{
		WinePrefix:       t.TempDir(),
		HookHistoryLimit: 10,
		HookHistoryLog: HookHistoryLogOptions{
			Enabled: true,
			GameDir: gameDir,
			Groups:  []string{"@dialogue.dll:1234"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	client.recordHookLine(&Line{Hook: "Thread A@dialogue.dll:1234", Speaker: "Alice", Text: "kept"})
	client.recordHookLine(&Line{Hook: "Thread B@names.dll:9999", Text: "ignored"})

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(gameDir, "logs", "dialogue.dll_1234.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	var entry hookHistoryLogEntry
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatal(err)
	}
	if entry.HookGroup != "@dialogue.dll:1234" || entry.Speaker != "Alice" || entry.Text != "kept" {
		t.Fatalf("unexpected log entry: %#v", entry)
	}

	ignoredPath := filepath.Join(gameDir, "logs", "names.dll_9999.jsonl")
	if _, err := os.Stat(ignoredPath); !os.IsNotExist(err) {
		t.Fatalf("expected ignored group not to be logged, stat err: %v", err)
	}
}

func TestHookHistoryLogRequiresDirectory(t *testing.T) {
	_, err := NewClient(ClientOptions{
		WinePrefix: t.TempDir(),
		HookHistoryLog: HookHistoryLogOptions{
			Enabled: true,
		},
	})
	if err == nil {
		t.Fatal("expected missing hook history log directory error")
	}
}

func readFeedLine(t *testing.T, feed <-chan *Line) *Line {
	t.Helper()

	select {
	case line, ok := <-feed:
		if !ok {
			t.Fatal("feed closed")
		}
		return line
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for feed line")
		return nil
	}
}
