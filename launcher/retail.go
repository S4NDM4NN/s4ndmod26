package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Retail upgrade: the launcher installs the free demo assets by default.  An owner of the game can point it
// at their own RTCW folder; the official paks are verified against retailPaks, copied into <install>/main
// and the demo pak is removed.  Nothing retail is ever downloaded.

func retailInfo(name string) (retailPak, bool) {
	for _, p := range retailPaks {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return retailPak{}, false
}

func retailSize(name string) int64 {
	p, _ := retailInfo(name)
	return p.Size
}

// retailInstalled reports whether the install's main folder already holds a complete set of retail paks
// (by name and size; they were hash-verified when they were imported).
func retailInstalled(dir string) bool {
	for _, n := range retailRequired {
		st, err := os.Stat(filepath.Join(dir, "main", n))
		if err != nil || !st.Mode().IsRegular() || st.Size() != retailSize(n) {
			return false
		}
	}
	return true
}

// applyEdition adjusts a manifest to what is on disk: a retail install does not want the demo pak, so it is
// dropped from the files to install and added to the ones to remove.
func applyEdition(m *Manifest, dir string) {
	if !retailInstalled(dir) {
		return
	}
	files := m.Files[:0:0]
	for _, f := range m.Files {
		if f.Path != "main/demopak0.pk3" {
			files = append(files, f)
		}
	}
	m.Files = files
	m.Prune = append(append([]string(nil), m.Prune...), "main/demopak0.pk3")
}

// resolveMain accepts the RTCW folder, or its Main folder, in any letter case.
func resolveMain(dir string) string {
	if hasFileFold(dir, "pak0.pk3") {
		return dir
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if e.IsDir() && strings.EqualFold(e.Name(), "main") {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return dir
}

func hasFileFold(dir, name string) bool { return findFold(dir, name) != "" }

func findFold(dir, name string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

var vdfPath = regexp.MustCompile(`"path"\s+"([^"]+)"`)

// findRetailFolders returns RTCW installs found in the usual places (Steam libraries, GOG, the original
// installer's folder), best guess first.  Only folders that actually hold pak0.pk3 are returned.
func findRetailFolders() []string {
	var cands []string
	home, _ := os.UserHomeDir()
	var steams []string
	switch platformName() {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "ProgramW6432"} {
			if p := os.Getenv(env); p != "" {
				steams = append(steams, filepath.Join(p, "Steam"))
				cands = append(cands,
					filepath.Join(p, "Return to Castle Wolfenstein", "Main"),
					filepath.Join(p, "GOG Galaxy", "Games", "Return to Castle Wolfenstein", "Main"),
					filepath.Join(p, "GOG.com", "Return to Castle Wolfenstein", "Main"))
			}
		}
		for _, d := range []string{"C", "D", "E", "F"} {
			steams = append(steams, d+`:\Steam`, d+`:\SteamLibrary`)
			cands = append(cands, d+`:\GOG Games\Return to Castle Wolfenstein\Main`)
		}
	default:
		steams = append(steams, filepath.Join(home, ".steam", "steam"), filepath.Join(home, ".local", "share", "Steam"))
	}
	for _, s := range steams {
		cands = append(cands, filepath.Join(s, "steamapps", "common", "Return to Castle Wolfenstein", "Main"))
		if b, err := os.ReadFile(filepath.Join(s, "steamapps", "libraryfolders.vdf")); err == nil {
			for _, m := range vdfPath.FindAllStringSubmatch(string(b), -1) {
				lib := strings.ReplaceAll(m[1], `\\`, `\`)
				cands = append(cands, filepath.Join(lib, "steamapps", "common", "Return to Castle Wolfenstein", "Main"))
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range cands {
		if r := resolveMain(c); !seen[r] && hasFileFold(r, "pak0.pk3") {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// RetailPlan is what an import would copy, found by reading (and hashing) the source folder.
type RetailPlan struct {
	Src     string
	Files   []retailPak // verified, in copy order
	Skipped []string    // optional packs that were present but are not the official file
	Bytes   int64
}

// planRetailImport verifies the source folder: every required pak must be present and exactly the official
// file; optional map packs are included when they verify and skipped (with a note) when they do not.
func planRetailImport(src string, progress func(name string)) (*RetailPlan, error) {
	src = resolveMain(src)
	pl := &RetailPlan{Src: src}
	for _, p := range retailPaks {
		req := false
		for _, r := range retailRequired {
			req = req || r == p.Name
		}
		f := findFold(src, p.Name)
		if f == "" {
			if req {
				return nil, fmt.Errorf("%s is missing from %s", p.Name, src)
			}
			continue
		}
		if progress != nil {
			progress(p.Name)
		}
		st, err := os.Stat(f)
		ok := err == nil && st.Size() == p.Size
		if ok {
			sum, herr := hashFile(f)
			ok = herr == nil && strings.EqualFold(sum, p.SHA256)
		}
		if !ok {
			if req {
				return nil, fmt.Errorf("%s in %s is not the official 1.41 file", p.Name, src)
			}
			pl.Skipped = append(pl.Skipped, p.Name)
			continue
		}
		pl.Files = append(pl.Files, p)
		pl.Bytes += p.Size
	}
	return pl, nil
}

// copyRetail copies the verified files into <dir>/main (via .part, re-hashing what was written) and records
// each one in the import journal, then removes the demo pak.  say gets one line per file, by name.  A failure
// removes the files this run copied and leaves no half-installed retail set behind.
func copyRetail(pl *RetailPlan, dir string, progress func(name string, n int64), say func(cls, text string)) error {
	if say == nil {
		say = func(string, string) {}
	}
	if err := checkImportSource(pl.Src, dir); err != nil {
		return err
	}
	dst := filepath.Join(dir, "main")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	j := loadJournal(dir)
	if err := j.save(dir); err != nil {
		return err
	}
	var written []string
	fail := func(err error) error {
		for _, w := range written {
			os.Remove(w)
			for i, c := range j.Created {
				if c.Name == filepath.Base(w) {
					j.Created = append(j.Created[:i], j.Created[i+1:]...)
					break
				}
			}
		}
		j.save(dir)
		return err
	}
	for _, p := range pl.Files {
		from := findFold(pl.Src, p.Name)
		to := filepath.Join(dst, p.Name)
		if fileMatches(to, p) {
			j.addPresent(p.Name)
			say("d", "already installed: "+p.Name)
			continue
		}
		if err := copyVerified(from, to, p, func(n int64) {
			if progress != nil {
				progress(p.Name, n)
			}
		}); err != nil {
			return fail(fmt.Errorf("%s: %w", p.Name, err))
		}
		written = append(written, to)
		j.addCreated(p.Name, p.Size)
		if err := j.save(dir); err != nil {
			return fail(err)
		}
		say("", fmt.Sprintf("copied %s (%.1f MB)", p.Name, float64(p.Size)/1048576))
	}
	demo := filepath.Join(dst, "demopak0.pk3")
	if _, err := os.Stat(demo); err == nil {
		if err := os.Remove(demo); err != nil {
			say("w", "could not remove demopak0.pk3: "+err.Error())
		} else {
			j.RemovedDemo = append(j.RemovedDemo, "demopak0.pk3")
			say("", "removed demopak0.pk3")
		}
	}
	return j.save(dir)
}

// fileMatches reports whether path is exactly the official file p (size and sha256).
func fileMatches(path string, p retailPak) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() != p.Size {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), p.SHA256)
}

func copyVerified(from, to string, p retailPak, progress func(n int64)) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	part := to + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	var n int64
	buf := make([]byte, 1<<20)
	for {
		k, rerr := in.Read(buf)
		if k > 0 {
			if _, werr := out.Write(buf[:k]); werr != nil {
				out.Close()
				os.Remove(part)
				return werr
			}
			h.Write(buf[:k])
			n += int64(k)
			progress(int64(k))
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
	if n != p.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), p.SHA256) {
		os.Remove(part)
		return fmt.Errorf("the copy does not match the official file")
	}
	os.Remove(to)
	return os.Rename(part, to)
}
