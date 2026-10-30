package demostore

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

// Blobs is where raw demos and merged outputs are kept.  Local disk implements
// it now (FSBlobs); an S3 store (Garage) can take its place without the rest
// of the tool noticing.  Keys are slash-separated lowercase names.
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
var ErrNotFound = errors.New("demostore: no such blob")

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

// RawKey is where the demo with this sha256 (lowercase hex) is kept.  Keyed by
// content, so the same file uploaded twice is stored once.
func RawKey(sha string) string { return "raw/" + sha[:2] + "/" + sha }

// PathsKey is where the merged output of a match is kept, for the version of
// the match it was built from.  The version changes when the match's demos do.
func PathsKey(matchID string, version int) string {
	return fmt.Sprintf("paths/%s-v%d.json.gz", matchID, version)
}

// FSBlobs keeps blobs as files under a directory.
type FSBlobs struct {
	root string
}

// NewFSBlobs makes the directory if needed.
func NewFSBlobs(root string) (*FSBlobs, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &FSBlobs{root: root}, nil
}

func (b *FSBlobs) path(key string) (string, error) {
	if !keyRe.MatchString(key) || strings.Contains(key, "..") {
		return "", fmt.Errorf("demostore: bad blob key %q", key)
	}
	return filepath.Join(b.root, filepath.FromSlash(key)), nil
}

func (b *FSBlobs) Put(_ context.Context, key string, r io.Reader) error {
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

func (b *FSBlobs) Get(_ context.Context, key string) (io.ReadCloser, error) {
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

func (b *FSBlobs) Delete(_ context.Context, key string) error {
	p, err := b.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (b *FSBlobs) Exists(_ context.Context, key string) (bool, error) {
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
