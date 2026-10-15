package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Result summarises one sync run.
type Result struct {
	Downloaded []string // install-relative paths fetched this run
	UpToDate   int
	Pruned     []string // install-relative paths removed
}

// Progress is called while downloading; total may be 0 when unknown.
type Progress func(file string, done, total int64)

// fileOK reports whether dest already holds the manifest's bytes.  The size check comes first so a
// 65 MB pak is only hashed when it could possibly match.
func fileOK(dest string, f ManifestFile) bool {
	st, err := os.Stat(dest)
	if err != nil || !st.Mode().IsRegular() || st.Size() != f.Size {
		return false
	}
	sum, err := hashFile(dest)
	return err == nil && strings.EqualFold(sum, f.SHA256)
}

func hashFile(p string) (string, error) {
	fh, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Sync makes dir match the manifest: downloads missing or changed files and removes what the manifest
// says to prune.  Downloads go to <file>.part, are verified, then renamed over the target, so an
// interrupted run never leaves a half-written pak that looks complete.
func Sync(client *http.Client, server string, m *Manifest, dir string, dryRun bool, progress Progress) (*Result, error) {
	res := &Result{}
	keep := map[string]bool{}
	for _, f := range m.Files {
		keep[f.Path] = true
	}

	for _, f := range m.Files {
		dest := filepath.Join(dir, filepath.FromSlash(f.Path))
		if fileOK(dest, f) {
			res.UpToDate++
			if f.Exec {
				os.Chmod(dest, 0o755)
			}
			continue
		}
		res.Downloaded = append(res.Downloaded, f.Path)
		if dryRun {
			continue
		}
		if err := download(client, server, f, dest, progress); err != nil {
			return res, fmt.Errorf("%s: %w", f.Path, err)
		}
	}

	for _, g := range m.Prune {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(g)))
		for _, p := range matches {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if keep[rel] || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
				continue
			}
			if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
				continue
			}
			res.Pruned = append(res.Pruned, rel)
			if !dryRun {
				os.Remove(p)
			}
		}
	}
	return res, nil
}

func download(client *http.Client, server string, f ManifestFile, dest string, progress Progress) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if lastErr = downloadOnce(client, strings.TrimRight(server, "/")+f.URL, f, dest, progress); lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func downloadOnce(client *http.Client, url string, f ManifestFile, dest string, progress Progress) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	part := dest + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				os.Remove(part)
				return werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(f.Path, done, f.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(part)
			return rerr
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(part)
		return err
	}
	if done != f.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), f.SHA256) {
		os.Remove(part)
		return fmt.Errorf("downloaded file does not match the manifest (got %d bytes, want %d)", done, f.Size)
	}
	if f.Exec {
		os.Chmod(part, 0o755)
	}
	// Windows cannot rename over an existing file; remove it first (a running exe is rejected here,
	// which is the right outcome: nothing is half-replaced).
	os.Remove(dest)
	return os.Rename(part, dest)
}
