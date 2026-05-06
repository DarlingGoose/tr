package textractor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Arch string

const (
	ArchX86 Arch = "x86"
	ArchX64 Arch = "x64"
)

type Client struct {
	WinePath   string
	WinePrefix string
	WineUser   string

	// RootDir defaults to:
	// WINEPREFIX/drive_c/users/<WineUser>/Desktop/Textractor
	RootDir string

	Arch Arch

	Logger *slog.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu      sync.Mutex
	started bool
	closed  bool

	lines  chan *Line
	errs   chan error
	hookMu sync.RWMutex
	hooks  map[string]string

	hookHistoryLimit int
	hookHistory      map[string][]*Line
	hookSubscribers  map[uint64]hookSubscriber
	nextHookSubID    uint64
}

type ClientOptions struct {
	WinePath   string
	WinePrefix string
	WineUser   string
	RootDir    string
	Arch       Arch
	Logger     *slog.Logger

	// HookHistoryLimit controls how many previous lines are retained for each
	// hook group. Values <= 0 use the default limit.
	HookHistoryLimit int
}

type hookSubscriber struct {
	group string
	ch    chan *Line
}

const defaultHookHistoryLimit = 500

func NewClient(opts ClientOptions) (*Client, error) {
	if opts.WinePrefix == "" {
		return nil, errors.New("wine prefix is required")
	}

	if opts.WinePath == "" {
		opts.WinePath = "wine"
	}
	if opts.WineUser == "" {
		opts.WineUser = currentUserName()
	}
	if opts.WineUser == "" {
		return nil, errors.New("wine user is required")
	}

	if opts.Arch == "" {
		opts.Arch = ArchX86
	}
	if opts.Arch != ArchX86 && opts.Arch != ArchX64 {
		return nil, fmt.Errorf("unsupported arch %q", opts.Arch)
	}
	if opts.HookHistoryLimit <= 0 {
		opts.HookHistoryLimit = defaultHookHistoryLimit
	}

	root := opts.RootDir
	if root == "" {
		root = filepath.Join(opts.WinePrefix, "drive_c", "users", opts.WineUser, "Desktop", "Textractor")
	}

	return &Client{
		WinePath:         opts.WinePath,
		WinePrefix:       opts.WinePrefix,
		WineUser:         opts.WineUser,
		RootDir:          root,
		Arch:             opts.Arch,
		Logger:           opts.Logger,
		lines:            make(chan *Line, 256),
		errs:             make(chan error, 16),
		hooks:            make(map[string]string),
		hookHistoryLimit: opts.HookHistoryLimit,
		hookHistory:      make(map[string][]*Line),
		hookSubscribers:  make(map[uint64]hookSubscriber),
	}, nil
}

func (c *Client) CLIPath() string {
	return filepath.Join(c.RootDir, string(c.Arch), "TextractorCLI.exe")
}

func (c *Client) CLIDir() string {
	return filepath.Dir(c.CLIPath())
}

func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("client is closed")
	}
	if c.started {
		return nil
	}

	if !fileExists(c.CLIPath()) {
		return fmt.Errorf("TextractorCLI.exe not found: %s", c.CLIPath())
	}

	cmd := exec.CommandContext(ctx, c.WinePath, "TextractorCLI.exe")
	cmd.Dir = c.CLIDir()
	cmd.Env = appendCleanEnv("WINEPREFIX=" + c.WinePrefix)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start TextractorCLI: %w", err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr
	c.started = true

	go c.readOutput(stdout)
	go c.readStderr(stderr)
	go c.wait()

	return nil
}

func (c *Client) Attach(ctx context.Context, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	if err := c.Start(ctx); err != nil {
		return err
	}
	return c.Command(ctx, fmt.Sprintf("attach -P%d", pid))
}

func (c *Client) Detach(ctx context.Context, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	if err := c.Start(ctx); err != nil {
		return err
	}
	return c.Command(ctx, fmt.Sprintf("detach -P%d", pid))
}

func (c *Client) AddHook(ctx context.Context, pid int, hookCode string) error {
	hookCode = strings.TrimSpace(hookCode)
	if hookCode == "" {
		return errors.New("hook code is required")
	}
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}

	if err := c.Start(ctx); err != nil {
		return err
	}

	// TextractorCLI syntax generally expects:
	//   <hookcode> -P<pid>
	return c.Command(ctx, fmt.Sprintf("%s -P%d", hookCode, pid))
}

func (c *Client) Command(ctx context.Context, command string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("client is closed")
	}
	if !c.started || c.stdin == nil {
		return errors.New("client is not started")
	}

	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	if c.Logger != nil {
		c.Logger.Debug("textractor command", "command", command)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.stdin.Write(utf16LEBytes(command + "\n"))
		done <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("write TextractorCLI command: %w", err)
		}
		return nil
	}
}

func (c *Client) Hooks() map[string]string {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()

	hooks := make(map[string]string, len(c.hooks))
	for hook, text := range c.hooks {
		hooks[hook] = text
	}
	return hooks
}

func (c *Client) HookGroups() []string {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()

	groups := make([]string, 0, len(c.hookHistory))
	for group := range c.hookHistory {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func (c *Client) HookHistory(group string) []*Line {
	group = HookGroup(group)

	c.hookMu.RLock()
	defer c.hookMu.RUnlock()

	lines := c.hookHistory[group]
	history := make([]*Line, 0, len(lines))
	for _, line := range lines {
		history = append(history, line.Clone())
	}
	return history
}

func (c *Client) HookHistories() map[string][]*Line {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()

	histories := make(map[string][]*Line, len(c.hookHistory))
	for group, lines := range c.hookHistory {
		history := make([]*Line, 0, len(lines))
		for _, line := range lines {
			history = append(history, line.Clone())
		}
		histories[group] = history
	}
	return histories
}

func (c *Client) HookFeed(ctx context.Context, group string, replayHistory bool) <-chan *Line {
	group = HookGroup(group)
	out := make(chan *Line, 256)

	c.hookMu.Lock()
	if replayHistory {
		if group == "" {
			groups := make([]string, 0, len(c.hookHistory))
			for group := range c.hookHistory {
				groups = append(groups, group)
			}
			sort.Strings(groups)

		replayAll:
			for _, group := range groups {
				for _, line := range c.hookHistory[group] {
					select {
					case out <- line.Clone():
					default:
						break replayAll
					}
				}
			}
		} else {
		replayGroup:
			for _, line := range c.hookHistory[group] {
				select {
				case out <- line.Clone():
				default:
					break replayGroup
				}
			}
		}
	}

	id := c.nextHookSubID
	c.nextHookSubID++
	c.hookSubscribers[id] = hookSubscriber{
		group: group,
		ch:    out,
	}
	c.hookMu.Unlock()

	go func() {
		<-ctx.Done()

		c.hookMu.Lock()
		delete(c.hookSubscribers, id)
		close(out)
		c.hookMu.Unlock()
	}()

	return out
}

func (c *Client) RawLines() <-chan *Line {
	return c.lines
}

func (c *Client) LinesFiltered(filter HookFilter) <-chan *Line {
	return FilterLines(SpeakerMergedLines(c.lines), filter)
}

func (c *Client) Lines() <-chan *Line {
	return SpeakerMergedLines(c.lines)
}

func (c *Client) Errors() <-chan error {
	return c.errs
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	var errs []error

	if c.stdin != nil {
		if err := c.stdin.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(os.Interrupt)

		done := make(chan error, 1)
		go func() {
			done <- c.cmd.Wait()
		}()

		select {
		case <-time.After(2 * time.Second):
			_ = c.cmd.Process.Kill()
		case err := <-done:
			if err != nil && !isExpectedProcessExit(err) {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (c *Client) readOutput(r io.Reader) {
	defer close(c.lines)

	// TextractorCLI output may come in UTF-16LE chunks.
	// Read raw chunks and decode.
	br := bufio.NewReader(r)

	var pending []byte

	for {
		chunk := make([]byte, 4096)
		n, err := br.Read(chunk)
		if n > 0 {
			pending = append(pending, chunk[:n]...)

			text := decodeLikelyText(pending)
			lines, keep := splitCompleteLines(text)

			for _, line := range lines {
				line = strings.TrimRight(line, "\r\n")
				if strings.TrimSpace(line) == "" {
					continue
				}
				l, err := ParseTextractorLine(line)
				if err != nil {
					slog.Error("invalid line", "err", err, "l", line)
					continue
				}
				c.recordHookLine(&l)
				select {
				case c.lines <- &l:
				default:
					if c.Logger != nil {
						c.Logger.Warn("dropping textractor output line; channel full")
					}
				}
			}

			// This is intentionally simple. For production, keep undecoded trailing bytes.
			if keep == "" {
				pending = pending[:0]
			} else {
				pending = utf16LEBytes(keep)
			}
		}

		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.pushErr(fmt.Errorf("read TextractorCLI stdout: %w", err))
			}
			return
		}
	}
}

func (c *Client) recordHookLine(line *Line) {
	if line == nil {
		return
	}

	stored := line.Clone()
	group := stored.HookGroup()
	if group == "" {
		return
	}

	c.hookMu.Lock()
	defer c.hookMu.Unlock()

	c.hooks[stored.Hook] = stored.Text

	history := append(c.hookHistory[group], stored)
	if len(history) > c.hookHistoryLimit {
		copy(history, history[len(history)-c.hookHistoryLimit:])
		history = history[:c.hookHistoryLimit]
	}
	c.hookHistory[group] = history

	for _, sub := range c.hookSubscribers {
		if sub.group != "" && sub.group != group {
			continue
		}

		select {
		case sub.ch <- stored.Clone():
		default:
			if c.Logger != nil {
				c.Logger.Warn("dropping hook feed line; channel full", "hook_group", group)
			}
		}
	}
}

func (c *Client) readStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if c.Logger != nil {
			c.Logger.Debug("textractor stderr", "line", line)
		}
	}
	if err := sc.Err(); err != nil {
		c.pushErr(fmt.Errorf("read TextractorCLI stderr: %w", err))
	}
}

func (c *Client) wait() {
	err := c.cmd.Wait()

	c.mu.Lock()
	c.started = false
	c.mu.Unlock()

	if err != nil && !isExpectedProcessExit(err) {
		c.pushErr(fmt.Errorf("TextractorCLI exited: %w", err))
	}
}

func (c *Client) pushErr(err error) {
	select {
	case c.errs <- err:
	default:
		if c.Logger != nil {
			c.Logger.Warn("dropping textractor error; channel full", "error", err)
		}
	}
}

func decodeLikelyText(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if looksUTF16LE(b) {
		return decodeUTF16LEBytes(b)
	}
	return string(b)
}

func splitCompleteLines(s string) ([]string, string) {
	if s == "" {
		return nil, ""
	}

	parts := strings.SplitAfter(s, "\n")
	if len(parts) == 0 {
		return nil, ""
	}

	var lines []string
	for _, p := range parts {
		if strings.HasSuffix(p, "\n") {
			lines = append(lines, p)
		} else {
			return lines, p
		}
	}

	return lines, ""
}

func isExpectedProcessExit(err error) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func appendCleanEnv(extra ...string) []string {
	env := os.Environ()

	// Remove existing values for keys we are overriding.
	for _, e := range extra {
		k, _, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}

		dst := env[:0]
		prefix := k + "="
		for _, existing := range env {
			if !strings.HasPrefix(existing, prefix) {
				dst = append(dst, existing)
			}
		}
		env = dst
	}

	return append(env, extra...)
}
