# tr

Go helpers for running TextractorCLI through Wine, attaching it to a game
process, and reading extracted visual novel text as structured hook feeds.

The package currently focuses on Linux/Wine workflows:

- install the embedded Textractor files into a Wine prefix
- find Wine processes by executable name
- detect PE architecture for `x86` / `x64` TextractorCLI selection
- attach/detach TextractorCLI to a process
- parse Textractor output into `Line` values
- merge likely speaker-name lines into dialogue lines
- keep per-hook history and live hook feeds

## Requirements

- Go `1.26.2`
- Wine available as `wine`, or a custom `WinePath`
- `file` command for `DetectArchFromFileCommand`
- A Wine prefix containing the target game

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/DarlingGoose/tr/pkg/textractor"
)

func main() {
	ctx := context.Background()
	prefix := "/path/to/wine/prefix"
	gameExe := "/path/to/wine/prefix/drive_c/Games/Game/Game.exe"

	installer := textractor.Installer{}
	install, err := installer.Install(ctx, textractor.InstallOptions{
		WinePrefix: prefix,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("installed Textractor:", install.RootDir)

	lister := textractor.ProcessLister{WinePrefix: prefix}
	procs, err := lister.FindByName(ctx, "Game.exe")
	if err != nil {
		log.Fatal(err)
	}
	if len(procs) == 0 {
		log.Fatal("game process not found")
	}

	arch, err := textractor.DetectArchFromFileCommand(ctx, gameExe)
	if err != nil {
		arch = textractor.ArchX86
	}

	client, err := textractor.NewClient(textractor.ClientOptions{
		WinePrefix: prefix,
		Arch:       arch,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if err := client.Attach(ctx, procs[0].PID); err != nil {
		log.Fatal(err)
	}

	lines := client.LinesFiltered(textractor.NewHookFilter("@13F548:Game.exe"))
	for line := range lines {
		fmt.Printf("%s: %s\n", line.Speaker, line.Text)
	}
}
```

## Hook Filters

Textractor often emits many hooks at once. Use `NewHookFilter` to allow one or
more hook groups:

```go
filter := textractor.NewHookFilter("@13F548:Game.exe")
lines := client.LinesFiltered(filter)
```

Hook groups are the stable suffix after the final `@` in a Textractor hook. For
example, `Thread A@13F548:Game.exe` becomes `@13F548:Game.exe`.

## Speaker Merging

`Lines()` and `LinesFiltered(...)` pass raw Textractor lines through speaker
merging. The merger:

- treats short name-like lines as pending speakers
- attaches a pending speaker to the next dialogue line
- supports games where name and dialogue arrive on separate hooks
- ignores symbol-only junk such as `\͎`
- briefly holds speakerless dialogue so a speaker that arrives just after it can
  still be attached

Use `RawLines()` if you need unmerged Textractor output.

## Best Dialogue Selection

Some games emit good text from more than one hook, or occasionally produce junk
from an otherwise useful hook. Use `BestDialogueLines` to listen across hooks and
emit the best readable line from each short burst:

```go
lines := client.BestDialogueLines()
for line := range lines {
	fmt.Printf("%s: %s\n", line.Speaker, line.Text)
}
```

The selector waits briefly for competing hooks, scores candidate lines, rejects
obvious junk, prefers complete quoted dialogue, and suppresses shorter duplicate
fragments after a full line was emitted. Tune the wait window if a game emits
alternate hooks more slowly:

```go
lines := client.BestDialogueLinesWithOptions(textractor.DialogueSelectorOptions{
	SelectionWindow: 150 * time.Millisecond,
})
```

## Hook History And Live Feeds

The client records recent lines per hook group while it reads Textractor output.
The default retention is 500 lines per hook group.

```go
client, err := textractor.NewClient(textractor.ClientOptions{
	WinePrefix:       prefix,
	Arch:             textractor.ArchX86,
	HookHistoryLimit: 1000,
})
```

Persist selected hook histories in the background with JSONL files under the
game directory's `logs` folder:

```go
client, err := textractor.NewClient(textractor.ClientOptions{
	WinePrefix: prefix,
	Arch:       textractor.ArchX86,
	HookHistoryLog: textractor.HookHistoryLogOptions{
		Enabled: true,
		GameDir: filepath.Dir(gameExe),
		Groups:  []string{"@13F548:Game.exe"},
	},
})
```

Pass no `Groups` to persist every hook group. Set `Dir` to override the output
directory.

Inspect known hook groups:

```go
groups := client.HookGroups()
```

Read previous data for a hook:

```go
history := client.HookHistory("@13F548:Game.exe")
for _, line := range history {
	fmt.Println(line.Text)
}
```

Subscribe to one live hook feed and optionally replay history first:

```go
feedCtx, cancel := context.WithCancel(ctx)
defer cancel()

feed := client.HookFeed(feedCtx, "@13F548:Game.exe", true)
for line := range feed {
	fmt.Println(line.Text)
}
```

Pass an empty group to `HookFeed` to receive all hook groups.

## Manual Hooks

If Textractor needs an explicit hook code, send it with `AddHook`:

```go
if err := client.AddHook(ctx, pid, "/HSN-4@13F548"); err != nil {
	log.Fatal(err)
}
```

## Development

Run tests with writable Go cache directories:

```sh
env GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache GOFLAGS=-mod=vendor go test ./...
```

Build the example command:

```sh
go build ./...
```

## Notes

This repository vendors its Go dependencies and embeds a Textractor archive under
`pkg/textractor/assets`. The top-level `main.go` is currently an example wired to
a local Wine prefix and game executable; adjust those paths before running it.
