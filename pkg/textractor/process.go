package textractor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type WineProcess struct {
	ImageName string
	PID       int
	Session   string
	SessionID string
	MemUsage  string
}

type ProcessLister struct {
	WinePath   string
	WinePrefix string
}

func (l ProcessLister) List(ctx context.Context) ([]WineProcess, error) {
	wine := l.WinePath
	if wine == "" {
		wine = "wine"
	}
	if l.WinePrefix == "" {
		return nil, fmt.Errorf("wine prefix is required")
	}

	cmd := exec.CommandContext(ctx, wine, "tasklist")
	cmd.Env = appendCleanEnv("WINEPREFIX=" + l.WinePrefix)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wine tasklist failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return parseTasklist(stdout.String()), nil
}

func (l ProcessLister) FindByName(ctx context.Context, name string) ([]WineProcess, error) {
	procs, err := l.List(ctx)
	if err != nil {
		return nil, err
	}

	name = strings.ToLower(name)
	var out []WineProcess
	for _, p := range procs {
		if strings.Contains(strings.ToLower(p.ImageName), name) {
			out = append(out, p)
		}
	}

	return out, nil
}

var tasklistLineRE = regexp.MustCompile(`^(.+?)\s+([0-9]+)\s+(.+?)\s+([0-9]+)\s+(.+)$`)

func parseTasklist(s string) []WineProcess {
	sc := bufio.NewScanner(strings.NewReader(s))

	var out []WineProcess
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if line == "" ||
			strings.HasPrefix(line, "Image Name") ||
			strings.HasPrefix(line, "====") {
			continue
		}

		m := tasklistLineRE.FindStringSubmatch(line)
		if len(m) != 6 {
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(m[2]))
		if err != nil {
			continue
		}

		out = append(out, WineProcess{
			ImageName: strings.TrimSpace(m[1]),
			PID:       pid,
			Session:   strings.TrimSpace(m[3]),
			SessionID: strings.TrimSpace(m[4]),
			MemUsage:  strings.TrimSpace(m[5]),
		})
	}

	return out
}
