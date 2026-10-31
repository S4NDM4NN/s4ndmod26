package blobstore

import (
	"context"
	"errors"
	"os"
	"strings"
)

// FromEnv opens the store that <prefix>_* in the environment asks for.  Local
// disk is the default: <prefix>_DIR (or defaultDir) is a folder to write to.
// Setting <prefix>_S3_BUCKET chooses a bucket instead, with
//
//	<prefix>_S3_ENDPOINT    host:port of the S3 service, no scheme
//	<prefix>_S3_ACCESS_KEY, <prefix>_S3_SECRET_KEY
//	<prefix>_S3_REGION      optional (default "garage")
//	<prefix>_S3_INSECURE=1  plain HTTP, for a service inside the cluster
//	<prefix>_S3_KEY_PREFIX  optional folder inside the bucket
//
// Asking for both a bucket and a folder is refused: a half-set-up deployment
// should not quietly write to the wrong place.  The second result describes
// the store for the log (it holds no secrets).
func FromEnv(ctx context.Context, prefix, defaultDir string) (Blobs, string, error) {
	get := func(name string) string { return os.Getenv(prefix + "_" + name) }
	if bucket := get("S3_BUCKET"); bucket != "" {
		if get("DIR") != "" {
			return nil, "", errors.New(prefix + "_S3_BUCKET and " + prefix + "_DIR are both set; use one")
		}
		s, err := NewS3(ctx, S3Config{
			Endpoint:  get("S3_ENDPOINT"),
			Bucket:    bucket,
			AccessKey: get("S3_ACCESS_KEY"),
			SecretKey: get("S3_SECRET_KEY"),
			Region:    get("S3_REGION"),
			Insecure:  get("S3_INSECURE") == "1",
			KeyPrefix: get("S3_KEY_PREFIX"),
		})
		if err != nil {
			return nil, "", err
		}
		return s, "bucket " + bucket + " at " + get("S3_ENDPOINT") + strings.TrimRight("/"+get("S3_KEY_PREFIX"), "/"), nil
	}
	dir := get("DIR")
	if dir == "" {
		dir = defaultDir
	}
	f, err := NewFS(dir)
	if err != nil {
		return nil, "", err
	}
	return f, dir, nil
}
