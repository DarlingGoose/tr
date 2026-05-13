package textractor

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

const defaultHookHistoryLogBuffer = 1024

type hookHistoryLogEntry struct {
	Time      time.Time `json:"time"`
	HookGroup string    `json:"hook_group"`
	Hook      string    `json:"hook"`
	Speaker   string    `json:"speaker,omitempty"`
	Text      string    `json:"text"`
	Raw       string    `json:"raw"`
}

type hookHistoryLogEvent struct {
	group string
	line  *Line
}

type hookHistoryLogger struct {
	dir       string
	groups    map[string]struct{}
	ch        chan hookHistoryLogEvent
	logger    *slog.Logger
	reportErr func(error)

	mu        sync.RWMutex
	closed    bool
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

func newHookHistoryLogger(opts HookHistoryLogOptions, logger *slog.Logger, reportErr func(error)) (*hookHistoryLogger, error) {
	if !opts.Enabled {
		return nil, nil
	}

	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		gameDir := strings.TrimSpace(opts.GameDir)
		if gameDir == "" {
			return nil, errors.New("hook history log game dir or dir is required")
		}
		dir = filepath.Join(gameDir, "logs")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create hook history log dir %s: %w", dir, err)
	}

	buffer := opts.Buffer
	if buffer <= 0 {
		buffer = defaultHookHistoryLogBuffer
	}

	groups := make(map[string]struct{}, len(opts.Groups))
	for _, group := range opts.Groups {
		group = HookGroup(group)
		if group != "" {
			groups[group] = struct{}{}
		}
	}

	l := &hookHistoryLogger{
		dir:       dir,
		groups:    groups,
		ch:        make(chan hookHistoryLogEvent, buffer),
		logger:    logger,
		reportErr: reportErr,
	}
	l.wg.Add(1)
	go l.run()

	return l, nil
}

func (l *hookHistoryLogger) Enqueue(group string, line *Line) {
	if l == nil || line == nil || group == "" {
		return
	}
	if len(l.groups) > 0 {
		if _, ok := l.groups[group]; !ok {
			return
		}
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return
	}

	select {
	case l.ch <- hookHistoryLogEvent{group: group, line: line.Clone()}:
	default:
		if l.logger != nil {
			l.logger.Warn("dropping hook history log line; queue full", "hook_group", group)
		}
	}
}

func (l *hookHistoryLogger) Close() error {
	if l == nil {
		return nil
	}

	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		close(l.ch)
		l.mu.Unlock()
		l.wg.Wait()
	})
	return l.closeErr
}

func (l *hookHistoryLogger) run() {
	defer l.wg.Done()

	files := make(map[string]*os.File)
	defer func() {
		for group, file := range files {
			if err := file.Close(); err != nil {
				l.setErr(fmt.Errorf("close hook history log %s: %w", group, err))
			}
		}
	}()

	for event := range l.ch {
		file, ok := files[event.group]
		if !ok {
			var err error
			file, err = os.OpenFile(l.pathForGroup(event.group), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				l.setErr(fmt.Errorf("open hook history log %s: %w", event.group, err))
				continue
			}
			files[event.group] = file
		}

		entry := hookHistoryLogEntry{
			Time:      time.Now().UTC(),
			HookGroup: event.group,
			Hook:      event.line.Hook,
			Speaker:   event.line.Speaker,
			Raw:       event.line.Raw,
			Text:      event.line.Text,
		}
		b, err := json.Marshal(entry)
		if err != nil {
			l.setErr(fmt.Errorf("marshal hook history log %s: %w", event.group, err))
			continue
		}
		b = append(b, '\n')

		if _, err := file.Write(b); err != nil {
			l.setErr(fmt.Errorf("write hook history log %s: %w", event.group, err))
		}
	}
}

func (l *hookHistoryLogger) pathForGroup(group string) string {
	return filepath.Join(l.dir, sanitizeHookHistoryLogName(group)+".jsonl")
}

func (l *hookHistoryLogger) setErr(err error) {
	if err == nil {
		return
	}
	if l.closeErr == nil {
		l.closeErr = err
	} else {
		l.closeErr = errors.Join(l.closeErr, err)
	}
	if l.reportErr != nil {
		l.reportErr(err)
	}
}

func sanitizeHookHistoryLogName(group string) string {
	group = strings.TrimSpace(group)
	group = strings.TrimPrefix(group, "@")

	var b strings.Builder
	lastUnderscore := false
	for _, r := range group {
		switch {
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
			lastUnderscore = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}

	name := strings.Trim(b.String(), "_")
	if name == "" {
		return "hook"
	}
	return name
}
