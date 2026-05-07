package textractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
	hookHistoryLog   *hookHistoryLogger
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

	// HookHistoryLog controls optional asynchronous persistence of hook history
	// lines. When enabled with GameDir and no Dir, logs are written under
	// GameDir/logs.
	HookHistoryLog HookHistoryLogOptions
}

type HookHistoryLogOptions struct {
	Enabled bool

	// GameDir is the game installation directory. If Dir is empty, hook history
	// logs are written under GameDir/logs.
	GameDir string

	// Dir overrides the output directory for hook history logs.
	Dir string

	// Groups limits persistence to these hook groups. Empty means all groups.
	Groups []string

	// Buffer controls the background logging queue size. Values <= 0 use the
	// default buffer.
	Buffer int
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

	client := &Client{
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
	}

	hookHistoryLog, err := newHookHistoryLogger(opts.HookHistoryLog, opts.Logger, client.pushErr)
	if err != nil {
		return nil, err
	}
	client.hookHistoryLog = hookHistoryLog

	return client, nil
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

	if c.hookHistoryLog != nil {
		if err := c.hookHistoryLog.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
