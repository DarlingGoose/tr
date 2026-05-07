package textractor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

type Installer struct {
	Logger *slog.Logger
}

type InstallOptions struct {
	// WinePrefix is required.
	// Example: /home/n9s/.config/vntext/prefixes/lpk-30003
	WinePrefix string

	// WineUser defaults to os.UserName(), then USER env, then "n9s"-style fallback is not guessed.
	// For your Wine prefix, this should usually be the Linux username.
	WineUser string

	// Force removes/replaces the existing Textractor directory.
	Force bool
}

type InstallResult struct {
	RootDir string
	X86Dir  string
	X64Dir  string
}

func (i Installer) Install(ctx context.Context, opts InstallOptions) (*InstallResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.WinePrefix == "" {
		return nil, errors.New("wine prefix is required")
	}

	wineUser := opts.WineUser
	if wineUser == "" {
		wineUser = currentUserName()
	}
	if wineUser == "" {
		return nil, errors.New("wine user is required")
	}

	root := filepath.Join(opts.WinePrefix, "drive_c", "users", wineUser, "Desktop", "Textractor")

	if opts.Force {
		if err := os.RemoveAll(root); err != nil {
			return nil, fmt.Errorf("remove existing textractor dir: %w", err)
		}
	}

	if fileExists(filepath.Join(root, "x86", "TextractorCLI.exe")) &&
		fileExists(filepath.Join(root, "x64", "TextractorCLI.exe")) {
		return &InstallResult{
			RootDir: root,
			X86Dir:  filepath.Join(root, "x86"),
			X64Dir:  filepath.Join(root, "x64"),
		}, nil
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create textractor root: %w", err)
	}

	if i.Logger != nil {
		i.Logger.Info("installing embedded Textractor", "root", root)
	}

	if err := extractEmbeddedTarGz(ctx, textractorTarGz, root); err != nil {
		return nil, err
	}

	// Support archives that contain either:
	//   Textractor/x86
	//   x86
	//
	// If archive extracted into root/Textractor/x86, move contents up into root.
	nested := filepath.Join(root, "Textractor")
	if dirExists(filepath.Join(nested, "x86")) || dirExists(filepath.Join(nested, "x64")) {
		if err := mergeDir(nested, root); err != nil {
			return nil, fmt.Errorf("merge nested Textractor dir: %w", err)
		}
		_ = os.RemoveAll(nested)
	}

	x86 := filepath.Join(root, "x86")
	x64 := filepath.Join(root, "x64")

	if !fileExists(filepath.Join(x86, "TextractorCLI.exe")) {
		return nil, fmt.Errorf("missing x86 TextractorCLI.exe after install: %s", filepath.Join(x86, "TextractorCLI.exe"))
	}
	if !fileExists(filepath.Join(x64, "TextractorCLI.exe")) {
		return nil, fmt.Errorf("missing x64 TextractorCLI.exe after install: %s", filepath.Join(x64, "TextractorCLI.exe"))
	}

	return &InstallResult{
		RootDir: root,
		X86Dir:  x86,
		X64Dir:  x64,
	}, nil
}

func extractEmbeddedTarGz(ctx context.Context, data []byte, dest string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	cleanDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		if hdr.Name == "" {
			continue
		}

		target, err := safeJoin(cleanDest, hdr.Name)
		if err != nil {
			return err
		}

		mode := fs.FileMode(hdr.Mode)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode.Perm()); err != nil {
				return fmt.Errorf("create dir %s: %w", target, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create parent dir %s: %w", filepath.Dir(target), err)
			}

			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
			if err != nil {
				return fmt.Errorf("create file %s: %w", target, err)
			}

			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()

			if copyErr != nil {
				return fmt.Errorf("write file %s: %w", target, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close file %s: %w", target, closeErr)
			}

		default:
			// Skip symlinks/devices/etc for safety.
			continue
		}
	}
}
