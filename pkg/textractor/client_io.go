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

			lines, keep := splitCompleteRawLines(pending)
			for _, rawLine := range lines {
				line := decodeLikelyText(rawLine)
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

			pending = keep
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

func splitCompleteRawLines(b []byte) ([][]byte, []byte) {
	if len(b) == 0 {
		return nil, nil
	}

	var lines [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}

		if likelyUTF16LEPrefix(b[start:i+1]) && i+1 == len(b) {
			break
		}

		end := i + 1
		if end < len(b) && b[end] == 0x00 {
			end++
		}

		line := make([]byte, end-start)
		copy(line, b[start:end])
		lines = append(lines, line)
		start = end
		i = end - 1
	}

	if start == len(b) {
		return lines, nil
	}

	keep := make([]byte, len(b)-start)
	copy(keep, b[start:])
	return lines, keep
}

func likelyUTF16LEPrefix(b []byte) bool {
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		return true
	}
	if len(b) < 4 {
		return false
	}

	var zeros int
	var pairs int
	for i := 1; i < len(b); i += 2 {
		pairs++
		if b[i] == 0 {
			zeros++
		}
	}

	return pairs > 0 && zeros*100/pairs > 40
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
