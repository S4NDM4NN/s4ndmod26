// Package blobstore keeps named blobs (demos, merged replays, game replays) on
// local disk or in an S3 bucket.  Local disk is the default; a bucket is chosen
// by configuration (see FromEnv) and the rest of the program does not notice.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Blobs is a store of named blobs.  Keys are slash-separated lowercase names.
type Blobs interface {
	// Put stores the bytes under key, replacing what is there.  A reader
	// never sees half a file.
	Put(ctx context.Context, key string, r io.Reader) error
	// Get opens a blob; the error is ErrNotFound if there is none.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes a blob; deleting what is not there is not an error.
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// ErrNotFound is returned for a key with no blob.
var ErrNotFound = errors.New("blobstore: no such blob")

// ValidKey reports whether key is a name the stores accept.
func ValidKey(key string) bool { return keyRe.MatchString(key) && !strings.Contains(key, "..") }

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

// FS keeps blobs as files under a directory.
type FS struct {
	root string
}

// NewFS makes the directory if needed.
func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &FS{root: root}, nil
}

func (b *FS) path(key string) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("blobstore: bad blob key %q", key)
	}
	return filepath.Join(b.root, filepath.FromSlash(key)), nil
}

func (b *FS) Put(_ context.Context, key string, r io.Reader) error {
	p, err := b.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (b *FS) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := b.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return f, err
}

func (b *FS) Delete(_ context.Context, key string) error {
	p, err := b.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (b *FS) Exists(_ context.Context, key string) (bool, error) {
	p, err := b.path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
