package demostore

import (
	"strings"
	"testing"

	"s4ndmod26/status-api/blobstore"
)

func TestKeysForBlobs(t *testing.T) {
	if k := RawKey(strings.Repeat("0f", 32)); !blobstore.ValidKey(k) || !strings.HasPrefix(k, "raw/0f/0f") {
		t.Errorf("RawKey = %q", k)
	}
	if k := PathsKey("a1b2c3d4e5f60718", 3); !blobstore.ValidKey(k) {
		t.Errorf("PathsKey = %q is not a valid key", k)
	}
}
