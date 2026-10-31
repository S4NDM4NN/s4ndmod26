package demostore

import (
	"fmt"

	"s4ndmod26/status-api/blobstore"
)

// Blobs is where raw demos and merged outputs are kept (see package blobstore).
type Blobs = blobstore.Blobs

// ErrNotFound is returned for a key with no blob.
var ErrNotFound = blobstore.ErrNotFound

// RawKey is where the demo with this sha256 (lowercase hex) is kept.  Keyed by
// content, so the same file uploaded twice is stored once.
func RawKey(sha string) string { return "raw/" + sha[:2] + "/" + sha }

// PathsKey is where the merged output of a match is kept, for the version of
// the match it was built from.  The version changes when the match's demos do.
func PathsKey(matchID string, version int) string {
	return fmt.Sprintf("paths/%s-v%d.json.gz", matchID, version)
}
