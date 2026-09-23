// SPDX-License-Identifier: Apache-2.0










package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)







var ErrNotFound = errors.New("blob: no bytes stored for that digest")

type Store struct{ dir string }

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("blob: create %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}





func (s *Store) path(digest string) string {
	if len(digest) < 2 {
		return filepath.Join(s.dir, "_bad", digest)
	}
	return filepath.Join(s.dir, digest[:2], digest)
}







func (s *Store) Put(r io.Reader, limit int64) (digest string, size int64, err error) {
	tmp, err := os.CreateTemp(s.dir, ".incoming-*")
	if err != nil {
		return "", 0, fmt.Errorf("blob: staging file: %w", err)
	}
	staged := tmp.Name()


	defer func() {
		tmp.Close()
		if err != nil {
			os.Remove(staged)
		}
	}()

	h := sha256.New()


	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit+1))
	if err != nil {
		return "", 0, fmt.Errorf("blob: write: %w", err)
	}
	if n > limit {
		err = fmt.Errorf("blob: %d bytes exceeds the %d-byte limit", n, limit)
		return "", n, err
	}
	if err = tmp.Sync(); err != nil {
		return "", n, fmt.Errorf("blob: sync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", n, fmt.Errorf("blob: close: %w", err)
	}

	digest = hex.EncodeToString(h.Sum(nil))
	dest := s.path(digest)
	if err = os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", n, fmt.Errorf("blob: create shard: %w", err)
	}



	if err = os.Rename(staged, dest); err != nil {
		return "", n, fmt.Errorf("blob: commit: %w", err)
	}
	_ = os.Chmod(dest, 0o600)
	return digest, n, nil
}


func (s *Store) Open(digest string) (io.ReadSeekCloser, int64, error) {
	f, err := os.Open(s.path(digest))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}




func (s *Store) Remove(digest string) error {
	err := os.Remove(s.path(digest))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
