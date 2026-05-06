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
	"strings"
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

func safeJoin(root, name string) (string, error) {
	name = filepath.Clean(filepath.FromSlash(name))

	if filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe absolute path in archive: %q", name)
	}

	target := filepath.Join(root, name)
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(root, cleanTarget)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return cleanTarget, nil
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", fmt.Errorf("unsafe path traversal in archive: %q", name)
	}

	return cleanTarget, nil
}

func mergeDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())

		_ = os.RemoveAll(to)

		if err := os.Rename(from, to); err == nil {
			continue
		}

		if e.IsDir() {
			if err := copyDir(from, to); err != nil {
				return err
			}
			_ = os.RemoveAll(from)
			continue
		}

		if err := copyFile(from, to); err != nil {
			return err
		}
		_ = os.Remove(from)
	}

	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}

		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()

	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func currentUserName() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
