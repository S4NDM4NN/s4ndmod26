package demostore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSBlobs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	b, err := NewFSBlobs(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	key := RawKey(strings.Repeat("ab", 32))
	if ok, _ := b.Exists(ctx, key); ok {
		t.Fatal("a blob exists before it was put")
	}
	if _, err := b.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a missing blob: %v, want ErrNotFound", err)
	}
	if err := b.Put(ctx, key, strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, key, strings.NewReader("second")); err != nil { // replaces
		t.Fatal(err)
	}
	r, err := b.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != "second" {
		t.Errorf("read %q, want the replacement", got)
	}
	if ok, _ := b.Exists(ctx, key); !ok {
		t.Error("a stored blob does not exist")
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, key); err != nil { // not an error to delete twice
		t.Errorf("second delete: %v", err)
	}
	if ok, _ := b.Exists(ctx, key); ok {
		t.Error("a deleted blob still exists")
	}
	// no half-written files are left behind
	filepath.Walk(filepath.Join(root, "store"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			t.Errorf("left over: %s", p)
		}
		return nil
	})
}

func TestFSBlobsRejectBadKeys(t *testing.T) {
	ctx := context.Background()
	b, _ := NewFSBlobs(t.TempDir())
	for _, key := range []string{"", "..", "../x", "a/../../x", "/etc/passwd", "a//b", "A/b", "a b", "raw/.hidden/x", "x/", "raw\\x"} {
		if err := b.Put(ctx, key, bytes.NewReader([]byte("x"))); err == nil {
			t.Errorf("Put accepted the key %q", key)
		}
		if _, err := b.Get(ctx, key); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Get of %q: %v, want a bad-key error", key, err)
		}
	}
}

func TestKeysForBlobs(t *testing.T) {
	if k := RawKey(strings.Repeat("0f", 32)); !keyRe.MatchString(k) || !strings.HasPrefix(k, "raw/0f/0f") {
		t.Errorf("RawKey = %q", k)
	}
	if k := PathsKey("a1b2c3d4e5f60718", 3); !keyRe.MatchString(k) {
		t.Errorf("PathsKey = %q is not a valid key", k)
	}
}
