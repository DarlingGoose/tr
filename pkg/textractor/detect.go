package textractor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func DetectArchFromFileCommand(ctx context.Context, exePath string) (Arch, error) {
	cmd := exec.CommandContext(ctx, "file", exePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("file command failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	out := stdout.String()

	switch {
	case strings.Contains(out, "PE32+"):
		return ArchX64, nil
	case strings.Contains(out, "PE32"):
		return ArchX86, nil
	default:
		return "", fmt.Errorf("could not detect PE arch from file output: %s", strings.TrimSpace(out))
	}
}
