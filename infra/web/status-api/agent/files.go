package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"s4ndmod26/status-api/replay"
)

// FileInfo is one entry of a listing.
type FileInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Live is set on a replay still being recorded: an .rpl with no .txt sidecar (the game writes the .txt at
	// intermission).  It is still growing; fetch it again, or with a Range, for more.
	Live bool `json:"live,omitempty"`
}

// fileDir serves the files of one folder whose names match pattern: a listing at the folder's root and each
// file by name (http.ServeContent: Range, If-Modified-Since).  Names are checked against the pattern, so a
// request can never leave the folder or reach any other kind of file.
type fileDir struct {
	dir     string
	pattern *regexp.Regexp
	live    func(dir, name string) bool
	// clip, when set, may answer a request itself (it reports whether it did), before the name is served as a file.
	clip func(w http.ResponseWriter, r *http.Request, dir, name string) bool
}

var (
	replayName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(rpl|txt|json)$`)
	statsName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.jsonl$`)
)

func replayLive(dir, name string) bool {
	if !strings.HasSuffix(name, ".rpl") {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, strings.TrimSuffix(name, ".rpl")+".txt"))
	return os.IsNotExist(err)
}

func (d fileDir) list(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(d.dir)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "cannot read folder", http.StatusInternalServerError)
		return
	}
	out := []FileInfo{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !d.pattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fi := FileInfo{Name: e.Name(), Size: info.Size(), Modified: info.ModTime().UTC()}
		if d.live != nil {
			fi.Live = d.live(d.dir, e.Name())
		}
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

func (d fileDir) file(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !d.pattern.MatchString(name) || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	if d.clip != nil && d.clip(w, r, d.dir, name) {
		return
	}
	serveFile(w, r, filepath.Join(d.dir, name), "no-cache")
}

// clipName is a play of the game or highlight slice of a recording: "<game>.potg.rpl" or "<game>.hl<slot>.rpl".
var clipName = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*?)\.(potg|hl([0-9]{1,3}))\.rpl$`)

// serveClip answers "<game>.potg.rpl" / "<game>.hl<slot>.rpl" with a small .rpl cut from the full recording: only
// the chunks the viewer loads for that clip (replay.SliceRPL), so a browser does not download the whole match.
// It is cut on request (a few hundred KB of copying) and never stored, so it appears in no listing and is never
// pruned or scanned.  A real file of that name, an unfinished recording (no .txt yet) or a game without that clip
// is a plain 404, and the viewer falls back to the full file.
func serveClip(w http.ResponseWriter, r *http.Request, dir, name string) bool {
	m := clipName.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	game := m[1]
	kind, slot := replay.ClipPOTG, 0
	if m[3] != "" {
		kind = replay.ClipHighlight
		slot, _ = strconv.Atoi(m[3])
	}
	full := filepath.Join(dir, game+".rpl")
	txt := filepath.Join(dir, game+".txt")
	rst, err1 := os.Lstat(full)
	tst, err2 := os.Lstat(txt)
	if err1 != nil || err2 != nil || !rst.Mode().IsRegular() || !tst.Mode().IsRegular() {
		http.NotFound(w, r)
		return true
	}
	from, to, err := replay.ClipWindow(txt, kind, slot)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	out, err := replay.SliceRPL(full, from, to)
	if err != nil {
		http.Error(w, "cannot cut the clip", http.StatusInternalServerError)
		return true
	}
	// a finished game's files do not change, so a browser or CDN may keep the clip for good
	w.Header().Set("Cache-Control", "public, max-age=86400")
	mod := rst.ModTime()
	if tst.ModTime().After(mod) {
		mod = tst.ModTime()
	}
	http.ServeContent(w, r, name, mod, bytes.NewReader(out))
	return true
}

// serveFile serves a regular file (not a link, not a folder), with Range support.
func serveFile(w http.ResponseWriter, r *http.Request, path, cacheControl string) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

// downloads serves the server's own pk3 and latest.txt under /dl/<mod>/, which is where sv_dlURL can point
// ("<agent>/dl"): clients download the exact pk3 this server runs.
type downloads struct {
	modDir string
	mod    string
	pk3    *regexp.Regexp
}

func newDownloads(c Config) downloads {
	return downloads{
		modDir: c.ModDir(),
		mod:    c.Mod,
		pk3:    regexp.MustCompile(`^` + regexp.QuoteMeta(c.Mod) + `_[A-Za-z0-9._-]+\.pk3$`),
	}
}

func (d downloads) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("mod") != d.mod {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	switch {
	case name == "latest.txt":
		serveFile(w, r, filepath.Join(d.modDir, name), "no-store")
	case d.pk3.MatchString(name) && !strings.Contains(name, ".."):
		// versioned: the content of a name never changes
		w.Header().Set("Content-Disposition", "attachment")
		serveFile(w, r, filepath.Join(d.modDir, name), "public, max-age=31536000, immutable")
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
