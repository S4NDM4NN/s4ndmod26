package blobstore

import (
	"bytes"
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

// S3 keeps blobs as objects in a bucket.  Garage, MinIO and AWS all work: the
// bucket is addressed by path, which is what Garage serves.
type S3 struct {
	c      *minio.Client
	bucket string
	prefix string
}

// NewS3 connects to the bucket and checks that it is there.
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("blobstore: an S3 store needs an endpoint, a bucket and an access key and secret")
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

// Put replaces the blob; readers see the old one or the whole new one.
func (b *S3) Put(ctx context.Context, key string, r io.Reader) error {
	n, err := b.name(key)
	if err != nil {
		return err
	}
	// A blob that fits in memory goes up in one request (the size has to be
	// known for that); a larger one streams in parts.
	head, err := io.ReadAll(io.LimitReader(r, singlePutMax+1))
	if err != nil {
		return err
	}
	if len(head) <= singlePutMax {
		_, err = b.c.PutObject(ctx, b.bucket, n, bytes.NewReader(head), int64(len(head)), minio.PutObjectOptions{})
		return err
	}
	_, err = b.c.PutObject(ctx, b.bucket, n, io.MultiReader(bytes.NewReader(head), r), -1, minio.PutObjectOptions{})
	return err
}

// singlePutMax is the largest blob uploaded in one request.
const singlePutMax = 16 << 20

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

func isMissing(err error) bool {
	if err == nil {
		return false
	}
	return minio.ToErrorResponse(err).Code == "NoSuchKey" || minio.ToErrorResponse(err).StatusCode == 404
}
