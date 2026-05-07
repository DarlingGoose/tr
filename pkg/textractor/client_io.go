package textractor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

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
