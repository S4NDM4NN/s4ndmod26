package blobstore

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

// FromEnv opens the store that <prefix>_* in the environment asks for.  Local
// disk is the default: <prefix>_DIR (or defaultDir) is a folder to write to.
// Setting <prefix>_S3_BUCKET chooses a bucket instead, with
//
//	<prefix>_S3_ENDPOINT    host:port of the S3 service, no scheme
//	<prefix>_S3_ACCESS_KEY, <prefix>_S3_SECRET_KEY
//	<prefix>_S3_REGION      optional (default "garage")
//	<prefix>_S3_INSECURE=1  (or true, yes) plain HTTP, for a service inside the cluster
//	<prefix>_S3_KEY_PREFIX  optional folder inside the bucket
//
// Asking for both a bucket and a folder is refused: a half-set-up deployment
// should not quietly write to the wrong place.  The second result describes
// the store for the log (it holds no secrets).
func FromEnv(ctx context.Context, prefix, defaultDir string) (Blobs, string, error) {
	get := func(name string) string { return os.Getenv(prefix + "_" + name) }
	if get("S3_BUCKET") == "" {
		for _, n := range []string{"S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_REGION", "S3_KEY_PREFIX"} {
			if get(n) != "" {
				return nil, "", errors.New(prefix + "_" + n + " is set but " + prefix + "_S3_BUCKET is not")
			}
		}
	}
	if bucket := get("S3_BUCKET"); bucket != "" {
		if get("DIR") != "" {
			return nil, "", errors.New(prefix + "_S3_BUCKET and " + prefix + "_DIR are both set; use one")
		}
		s, err := openS3(ctx, S3Config{
			Endpoint:  get("S3_ENDPOINT"),
			Bucket:    bucket,
			AccessKey: get("S3_ACCESS_KEY"),
			SecretKey: get("S3_SECRET_KEY"),
			Region:    get("S3_REGION"),
			Insecure:  isTrue(get("S3_INSECURE")),
			KeyPrefix: get("S3_KEY_PREFIX"),
		})
		if err != nil {
			return nil, "", err
		}
		where := "bucket " + bucket + " at " + get("S3_ENDPOINT")
		if kp := strings.Trim(get("S3_KEY_PREFIX"), "/"); kp != "" {
			where += " under " + kp + "/"
		}
		return s, where, nil
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

func isTrue(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// openS3 connects, trying for a while: the store may be starting up beside the
// program (or across a link that is slow to come up), and nothing retries later.
func openS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if err := cfg.check(); err != nil { // no waiting will fix that
		return nil, err
	}
	var err error
	for i := 0; i < 6; i++ {
		var s *S3
		if s, err = NewS3(ctx, cfg); err == nil {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(5 * time.Second):
		}
	}
	return nil, err
}
