package accounts

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP's minimum for argon2id: 19 MiB, 2 passes, 1 lane).  They are written into every hash,
// so they can be raised later and old hashes still verify.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

const (
	MinPassword = 10
	MaxPassword = 128 // keeps one request from making the server hash a megabyte
)

var errBadHash = errors.New("not an argon2id hash")

// hashPassword returns a PHC string: $argon2id$v=19$m=..,t=..,p=..$salt$hash.
func hashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(sum)), nil
}

// checkPassword compares pw with a hash from hashPassword.
func checkPassword(pw, encoded string) (bool, error) {
	f := strings.Split(encoded, "$")
	if len(f) != 6 || f[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(f[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(f[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 || m > 256*1024 || t > 10 {
		return false, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(f[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(f[5])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
