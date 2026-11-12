package agent

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
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
	serveFile(w, r, filepath.Join(d.dir, name), "no-cache")
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
