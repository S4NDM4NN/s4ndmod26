package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config says where a bucket is and how to get into it.
type S3Config struct {
	Endpoint  string // host:port, no scheme (Garage: "garage:3900")
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string            // Garage wants the one it was set up with ("garage" by default)
	Insecure  bool              // plain HTTP, for a store inside the cluster
	KeyPrefix string            // kept in front of every key, so one bucket can hold several stores
	Transport http.RoundTripper // nil for the default; tests use it to trust a throwaway certificate
}

func (c S3Config) check() error {
	if c.Endpoint == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return errors.New("blobstore: an S3 store needs an endpoint, a bucket and an access key and secret")
	}
	return nil
}

// S3 keeps blobs as objects in a bucket.  Garage, MinIO and AWS all work: the
// bucket is addressed by path, which is what Garage serves.
type S3 struct {
	c      *minio.Client
	bucket string
	prefix string
}

// NewS3 connects to the bucket and checks that it is there.
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		cfg.Region = "garage"
	}
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       !cfg.Insecure,
		Region:       cfg.Region,
		BucketLookup: minio.BucketLookupPath,
		Transport:    cfg.Transport,
	})
	if err != nil {
		return nil, err
	}
	ok, err := c.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("blobstore: bucket %s: %w", cfg.Bucket, err)
	}
	if !ok {
		return nil, fmt.Errorf("blobstore: bucket %s does not exist", cfg.Bucket)
	}
	prefix := strings.Trim(cfg.KeyPrefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &S3{c: c, bucket: cfg.Bucket, prefix: prefix}, nil
}

func (b *S3) name(key string) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("blobstore: bad blob key %q", key)
	}
	return b.prefix + key, nil
}

// Put replaces the blob; readers see the old one or the whole new one.  A
// reader that knows its length (a bytes.Reader or strings.Reader) goes up with
// that length; any other is streamed in parts.
func (b *S3) Put(ctx context.Context, key string, r io.Reader) error {
	n, err := b.name(key)
	if err != nil {
		return err
	}
	size := int64(-1)
	if l, ok := r.(interface{ Len() int }); ok {
		size = int64(l.Len())
	}
	// Left alone, minio sizes the parts of a stream of unknown length for 5 TiB and
	// holds a buffer that big.
	_, err = b.c.PutObject(ctx, b.bucket, n, r, size, minio.PutObjectOptions{PartSize: partSize})
	return err
}

// partSize is how much of a blob goes up in one request.
const partSize = 16 << 20

func (b *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	n, err := b.name(key)
	if err != nil {
		return nil, err
	}
	o, err := b.c.GetObject(ctx, b.bucket, n, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject does not touch the network: a missing object shows on the first read
	if _, err := o.Stat(); err != nil {
		o.Close()
		if isMissing(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

func (b *S3) Delete(ctx context.Context, key string) error {
	n, err := b.name(key)
	if err != nil {
		return err
	}
	err = b.c.RemoveObject(ctx, b.bucket, n, minio.RemoveObjectOptions{})
	if err != nil && !isMissing(err) {
		return err
	}
	return nil
}

func (b *S3) Exists(ctx context.Context, key string) (bool, error) {
	n, err := b.name(key)
	if err != nil {
		return false, err
	}
	_, err = b.c.StatObject(ctx, b.bucket, n, minio.StatObjectOptions{})
	if isMissing(err) {
		return false, nil
	}
	return err == nil, err
}

// isMissing is true for a missing object, not for a missing bucket or any other fault.
func isMissing(err error) bool {
	return err != nil && minio.ToErrorResponse(err).Code == "NoSuchKey"
}
