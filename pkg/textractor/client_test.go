package textractor

import (
	"context"
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
