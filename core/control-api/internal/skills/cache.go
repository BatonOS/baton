// SPDX-License-Identifier: Apache-2.0















package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)







const MaxArchiveBytes int64 = 64 << 20

var (
	ErrDigestMalformed = errors.New("skills: the digest is not sha256:<64 hex>")
	ErrDigestMismatch  = errors.New("skills: the fetched bytes do not match the declared digest")
	ErrTooLarge        = errors.New("skills: the archive is larger than the limit")
	ErrNotCached       = errors.New("skills: no cached copy")
)






type Cache struct {
	Dir string



	Client *http.Client
}

func NewCache(dataDir string) *Cache {
	return &Cache{
		Dir: filepath.Join(dataDir, "skills"),
		Client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}








func (c *Cache) digestPath(digest string) (string, error) {
	rest, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(rest) != 64 {
		return "", fmt.Errorf("%w: %q", ErrDigestMalformed, digest)
	}
	if _, err := hex.DecodeString(rest); err != nil {
		return "", fmt.Errorf("%w: %q", ErrDigestMalformed, digest)
	}
	return filepath.Join(c.Dir, rest), nil
}


func (c *Cache) Has(digest string) bool {
	p, err := c.digestPath(digest)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}






func (c *Cache) Open(digest string) (*os.File, error) {
	p, err := c.digestPath(digest)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotCached
	}
	return f, err
}






func (c *Cache) Fetch(ctx context.Context, url, digest string) (int64, error) {
	target, err := c.digestPath(digest)
	if err != nil {
		return 0, err
	}
	if info, err := os.Stat(target); err == nil {
		return info.Size(), nil
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("skills: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("skills: %s answered %s", url, resp.Status)
	}




	tmp, err := os.CreateTemp(c.Dir, ".partial-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())

	sum := sha256.New()



	n, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(resp.Body, MaxArchiveBytes+1))
	tmp.Close()
	if err != nil {
		return 0, err
	}
	if n > MaxArchiveBytes {
		return 0, fmt.Errorf("%w: %s is past %d bytes", ErrTooLarge, url, MaxArchiveBytes)
	}

	got := "sha256:" + hex.EncodeToString(sum.Sum(nil))
	if got != digest {



		return 0, fmt.Errorf("%w: %s declared %s, served %s", ErrDigestMismatch, url, digest, got)
	}


	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return 0, err
	}
	return n, nil
}


















func (c *Cache) Put(digest string, r io.Reader) (int64, error) {
	target, err := c.digestPath(digest)
	if err != nil {
		return 0, err
	}
	if info, err := os.Stat(target); err == nil {
		return info.Size(), nil
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(c.Dir, ".partial-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	sum := sha256.New()


	n, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(r, MaxArchiveBytes+1))
	_ = tmp.Close()
	if err != nil {
		return 0, err
	}
	if n > MaxArchiveBytes {
		return 0, fmt.Errorf("%w: past %d bytes", ErrTooLarge, MaxArchiveBytes)
	}
	if got := "sha256:" + hex.EncodeToString(sum.Sum(nil)); got != digest {
		return 0, fmt.Errorf("%w: declared %s, received %s", ErrDigestMismatch, digest, got)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return 0, err
	}
	return n, nil
}
