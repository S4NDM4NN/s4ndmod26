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

// Plan is what a sync would do.  Building it only reads the install dir.
type Plan struct {
	Download   []ManifestFile
	Prune      []string // install-relative
	UpToDate   int
	BytesTotal int64 // sum of Download sizes
}

// MakePlan compares the install dir with the manifest.
func MakePlan(m *Manifest, dir string) *Plan {
	p := &Plan{}
	keep := map[string]bool{}
	for _, f := range m.Files {
		keep[f.Path] = true
	}
	for _, f := range m.Files {
		dest := filepath.Join(dir, filepath.FromSlash(f.Path))
		if fileOK(dest, f) {
			p.UpToDate++
			if f.Exec {
				os.Chmod(dest, 0o755)
			}
			continue
		}
		p.Download = append(p.Download, f)
		p.BytesTotal += f.Size
	}
	for _, g := range m.Prune {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(g)))
		for _, abs := range matches {
			rel, err := filepath.Rel(dir, abs)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if keep[rel] || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
				continue
			}
			if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
				continue
			}
			p.Prune = append(p.Prune, rel)
		}
	}
	return p
}

// Empty reports whether there is nothing the user needs to be told about.  The game unpacks its code
// libraries (cgame/ui .dll/.so) next to the mod pk3 each time it runs, so finding them is normal after
// playing and must not read as an update; they are still removed whenever an update is applied, so a
// stale one never outlives a new mod build.
func (p *Plan) Empty() bool {
	if len(p.Download) > 0 {
		return false
	}
	for _, rel := range p.Prune {
		if !isExtractedLib(rel) {
			return false
		}
	}
	return true
}

func isExtractedLib(rel string) bool {
	if !strings.HasPrefix(rel, "s4ndmod26/") {
		return false
	}
	l := strings.ToLower(rel)
	return strings.HasSuffix(l, ".dll") || strings.HasSuffix(l, ".so")
}

// Apply carries the plan out: downloads, then removals.
func (p *Plan) Apply(client *http.Client, server, dir string, progress Progress) error {
	for _, f := range p.Download {
		dest := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := download(client, server, f, dest, progress); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	for _, rel := range p.Prune {
		os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
	}
	return nil
}

// Sync makes dir match the manifest: downloads missing or changed files and removes what the manifest
// says to prune.  Downloads go to <file>.part, are verified, then renamed over the target, so an
// interrupted run never leaves a half-written pak that looks complete.
func Sync(client *http.Client, server string, m *Manifest, dir string, dryRun bool, progress Progress) (*Result, error) {
	p := MakePlan(m, dir)
	res := &Result{UpToDate: p.UpToDate, Pruned: p.Prune}
	for _, f := range p.Download {
		res.Downloaded = append(res.Downloaded, f.Path)
	}
	if dryRun {
		return res, nil
	}
	return res, p.Apply(client, server, dir, progress)
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
