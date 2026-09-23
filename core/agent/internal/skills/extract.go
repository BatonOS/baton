// SPDX-License-Identifier: Apache-2.0
























package skills

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)






const (



	MaxTotalBytes int64 = 64 << 20


	MaxFiles = 4096


	MaxRatio int64 = 200
)

var (
	ErrEscapes     = errors.New("skills: an entry would be written outside the destination")
	ErrTooLarge    = errors.New("skills: the archive expands beyond the size limit")
	ErrTooMany     = errors.New("skills: the archive has more entries than the limit")
	ErrHashMismatch = errors.New("skills: the archive does not hash to what was declared")
	ErrSymlink     = errors.New("skills: the archive contains a link, which is not unpacked")
)













func VerifyHash(data []byte, declared string) error {
	sum := sha256.Sum256(data)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, declared) {
		return fmt.Errorf("%w: declared %s, got %s", ErrHashMismatch, declared, got)
	}
	return nil
}









func Extract(archive []byte, dest string) (int, error) {
	root, err := filepath.Abs(dest)
	if err != nil {
		return 0, err
	}



	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	zr, err := zip.NewReader(newByteReader(archive), int64(len(archive)))
	if err != nil {
		return 0, fmt.Errorf("skills: not a readable archive: %w", err)
	}
	if len(zr.File) > MaxFiles {
		return 0, fmt.Errorf("%w: %d entries", ErrTooMany, len(zr.File))
	}

	var written int64
	files := 0
	for _, f := range zr.File {




		if f.Mode()&os.ModeSymlink != 0 {
			return files, fmt.Errorf("%w: %s", ErrSymlink, f.Name)
		}











		if strings.HasPrefix(f.Name, "/") || strings.HasPrefix(f.Name, `\`) ||
			(len(f.Name) > 1 && f.Name[1] == ':') {
			return files, fmt.Errorf("%w: %s is an absolute path", ErrEscapes, f.Name)
		}

		target := filepath.Join(root, filepath.FromSlash(f.Name))

		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return files, fmt.Errorf("%w: %s", ErrEscapes, f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return files, err
			}
			continue
		}



		if f.CompressedSize64 > 0 && int64(f.UncompressedSize64)/int64(f.CompressedSize64) > MaxRatio {
			return files, fmt.Errorf("%w: %s expands %dx", ErrTooLarge, f.Name,
				int64(f.UncompressedSize64)/int64(f.CompressedSize64))
		}
		if written+int64(f.UncompressedSize64) > MaxTotalBytes {
			return files, fmt.Errorf("%w: past %d bytes at %s", ErrTooLarge, MaxTotalBytes, f.Name)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return files, err
		}
		rc, err := f.Open()
		if err != nil {
			return files, err
		}


























		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return files, err
		}



		n, err := io.Copy(out, io.LimitReader(rc, MaxTotalBytes-written+1))
		rc.Close()
		out.Close()
		if err != nil {
			return files, err
		}
		written += n
		if written > MaxTotalBytes {
			return files, fmt.Errorf("%w: past %d bytes", ErrTooLarge, MaxTotalBytes)
		}
		files++
	}
	return files, nil
}

type byteReader struct{ b []byte }

func newByteReader(b []byte) *byteReader { return &byteReader{b: b} }

func (r *byteReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(r.b)) {
		return 0, io.EOF
	}
	n := copy(p, r.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
