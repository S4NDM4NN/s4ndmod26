// Package demostore holds what the demo-replay tool keeps: who may upload
// (keys), the raw demos and merged outputs (blobs), and the matches they make
// up (Postgres).
package demostore

import (
	"bufio"
	"crypto/subtle"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Keys are the uploader keys.  Each one has a label that names its owner: it
// is what ties an upload to the key that made it, so that the same key (and
// no other) can remove it again.
type Keys struct {
	entries []keyEntry
}

type keyEntry struct {
	label  string
	secret string
}

const minKeyLen = 16

var labelRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)

// LoadKeys reads a keys file: one "label secret" pair per line, blank lines
// and #-comments ignored.  legacy, if not empty, is one more key labelled
// "default" (the single shared key the first version of the tool used).
func LoadKeys(path, legacy string) (*Keys, error) {
	k := &Keys{}
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) != 2 {
				return nil, fmt.Errorf("%s:%d: want \"label secret\"", path, n)
			}
			if err := k.add(parts[0], parts[1]); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, n, err)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	if legacy != "" {
		if err := k.add("default", legacy); err != nil {
			return nil, fmt.Errorf("USERDEMO_UPLOAD_KEY: %w", err)
		}
	}
	return k, nil
}

func (k *Keys) add(label, secret string) error {
	if !labelRe.MatchString(label) {
		return fmt.Errorf("bad label %q (letters, digits, _ . - up to 40)", label)
	}
	if len(secret) < minKeyLen {
		return fmt.Errorf("the key for %q is shorter than %d characters", label, minKeyLen)
	}
	for _, e := range k.entries {
		if e.label == label {
			return fmt.Errorf("label %q is used twice", label)
		}
		if e.secret == secret {
			return fmt.Errorf("%q and %q have the same key", e.label, label)
		}
	}
	k.entries = append(k.entries, keyEntry{label, secret})
	return nil
}

// Len is how many keys there are.
func (k *Keys) Len() int {
	if k == nil {
		return 0
	}
	return len(k.entries)
}

// Lookup returns the label of the key equal to secret.  Every key is compared
// (in constant time each), so how long it takes does not say how close a guess was.
func (k *Keys) Lookup(secret string) (label string, ok bool) {
	if k == nil || secret == "" {
		return "", false
	}
	for _, e := range k.entries {
		if subtle.ConstantTimeCompare([]byte(secret), []byte(e.secret)) == 1 {
			label, ok = e.label, true
		}
	}
	return label, ok
}
