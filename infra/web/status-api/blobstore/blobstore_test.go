package blobstore

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const key = "raw/ab/" + "abababababababababababababababababababababababababababababababab"

// contract is what every store must do.
func contract(t *testing.T, b Blobs) {
	t.Helper()
	ctx := context.Background()
	if ok, err := b.Exists(ctx, key); ok || err != nil {
		t.Fatalf("a blob exists before it was put: %v %v", ok, err)
	}
	if _, err := b.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a missing blob: %v, want ErrNotFound", err)
	}
	if err := b.Delete(ctx, key); err != nil { // deleting what is not there is fine
		t.Fatalf("delete of a missing blob: %v", err)
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
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<21) // 32 MiB: sent in parts
	if err := b.Put(ctx, "paths/big.bin", bytes.NewReader(big)); err != nil {
		t.Fatalf("large put: %v", err)
	}
	r, err = b.Get(ctx, "paths/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, big) {
		t.Errorf("large blob came back as %d bytes, want %d", len(got), len(big))
	}
	b.Delete(ctx, "paths/big.bin")
	if err := b.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, key); err != nil { // not an error to delete twice
		t.Errorf("second delete: %v", err)
	}
	if ok, _ := b.Exists(ctx, key); ok {
		t.Error("a deleted blob still exists")
	}
	for _, bad := range []string{"", "..", "../x", "a/../../x", "/etc/passwd", "a//b", "A/b", "a b", "raw/.hidden/x", "x/", "raw\\x"} {
		if err := b.Put(ctx, bad, bytes.NewReader([]byte("x"))); err == nil {
			t.Errorf("Put accepted the key %q", bad)
		}
		if _, err := b.Get(ctx, bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Get of %q: %v, want a bad-key error", bad, err)
		}
	}
}

func TestFS(t *testing.T) {
	root := t.TempDir()
	b, err := NewFS(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	contract(t, b)
	// no half-written files are left behind
	filepath.Walk(filepath.Join(root, "store"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			t.Errorf("left over: %s", p)
		}
		return nil
	})
}

// fakeS3 is the few calls of S3 the store makes, in memory.
type fakeS3 struct {
	mu    sync.Mutex
	objs  map[string][]byte
	parts map[string][]byte
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, name, _ := strings.Cut(path, "/")
	if bucket != "bkt" {
		http.Error(w, "<Error><Code>NoSuchBucket</Code></Error>", http.StatusNotFound)
		return
	}
	if name == "" { // the bucket itself
		w.WriteHeader(http.StatusOK)
		return
	}
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		io.WriteString(w, `<?xml version="1.0"?><InitiateMultipartUploadResult><Bucket>bkt</Bucket><Key>k</Key><UploadId>u1</UploadId></InitiateMultipartUploadResult>`)
		return
	case r.Method == http.MethodPut && q.Get("uploadId") != "":
		b, _ := io.ReadAll(r.Body)
		f.parts[name+"#"+q.Get("partNumber")] = b
		w.Header().Set("ETag", `"p`+q.Get("partNumber")+`"`)
		w.WriteHeader(http.StatusOK)
		return
	case r.Method == http.MethodPost && q.Get("uploadId") != "":
		var all []byte
		for i := 1; ; i++ {
			p, ok := f.parts[name+"#"+strconv.Itoa(i)]
			if !ok {
				break
			}
			all = append(all, p...)
			delete(f.parts, name+"#"+strconv.Itoa(i))
		}
		f.objs[name] = all
		io.WriteString(w, `<?xml version="1.0"?><CompleteMultipartUploadResult><Bucket>bkt</Bucket><Key>k</Key><ETag>"x"</ETag></CompleteMultipartUploadResult>`)
		return
	}
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objs[name] = b
		w.Header().Set("ETag", `"x"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		b, ok := f.objs[name]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			if r.Method == http.MethodGet {
				io.WriteString(w, `<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>no</Message></Error>`)
			}
			return
		}
		w.Header().Set("ETag", `"x"`)
		http.ServeContent(w, r, name, time.Unix(1700000000, 0), bytes.NewReader(b))
	case http.MethodDelete:
		delete(f.objs, name)
		w.WriteHeader(http.StatusNoContent)
	}
}

func newFakeS3(t *testing.T, prefix string) (*S3, *fakeS3) {
	t.Helper()
	f := &fakeS3{objs: map[string][]byte{}, parts: map[string][]byte{}}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	b, err := NewS3(context.Background(), S3Config{
		Endpoint: strings.TrimPrefix(srv.URL, "https://"), Bucket: "bkt", AccessKey: "ak", SecretKey: "sk-sk-sk-sk",
		KeyPrefix: prefix, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b, f
}

func TestS3(t *testing.T) {
	b, f := newFakeS3(t, "demos")
	contract(t, b)
	b.Put(context.Background(), "paths/x", strings.NewReader("1"))
	if _, ok := f.objs["demos/paths/x"]; !ok {
		t.Errorf("objects are %v, want the key prefix in front", keysOf(f.objs))
	}
}

func keysOf(m map[string][]byte) (out []string) {
	for k := range m {
		out = append(out, k)
	}
	return
}

func TestS3NeedsABucketThatExists(t *testing.T) {
	f := &fakeS3{objs: map[string][]byte{}, parts: map[string][]byte{}}
	srv := httptest.NewTLSServer(f)
	defer srv.Close()
	_, err := NewS3(context.Background(), S3Config{
		Endpoint: strings.TrimPrefix(srv.URL, "https://"), Bucket: "other", AccessKey: "ak", SecretKey: "sk-sk-sk-sk",
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	})
	if err == nil {
		t.Error("a missing bucket was accepted")
	}
	if _, err := NewS3(context.Background(), S3Config{Bucket: "b"}); err == nil {
		t.Error("an S3 config with no endpoint or keys was accepted")
	}
}

func TestFromEnv(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("T_DIR", "")
	b, desc, err := FromEnv(ctx, "T", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.(*FS); !ok || desc != dir {
		t.Errorf("default store is %T (%s), want the folder %s", b, desc, dir)
	}
	t.Setenv("T_S3_BUCKET", "bkt")
	t.Setenv("T_DIR", dir)
	if _, _, err := FromEnv(ctx, "T", ""); err == nil {
		t.Error("a bucket and a folder together were accepted")
	}
	t.Setenv("T_DIR", "")
	if _, _, err := FromEnv(ctx, "T", dir); err == nil { // a bucket with no endpoint or keys
		t.Error("a bucket without an endpoint or keys was accepted")
	}
}

func TestKeys(t *testing.T) {
	for _, k := range []string{key, "paths/ab12-v3.json.gz", "a"} {
		if !ValidKey(k) {
			t.Errorf("%q should be a valid key", k)
		}
	}
}
