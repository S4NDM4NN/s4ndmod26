package replay

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// clipName is a play of the game or highlight slice of a recording: "<game>.potg.rpl" or "<game>.hl<slot>.rpl".
var clipName = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*?)\.(potg|hl([0-9]{1,3}))\.rpl$`)

// ServeClip answers "<game>.potg.rpl" / "<game>.hl<slot>.rpl" with a small .rpl cut from the full recording: only
// the chunks the viewer loads for that clip (SliceRPL), so a browser does not download the whole match.
// The agent and the site both serve it (a name is checked against clipName, so it cannot leave dir).  It is cut on request (a few hundred KB of copying) and never stored, so it appears in no listing and is never
// pruned or scanned.  A real file of that name, an unfinished recording (no .txt yet) or a game without that clip
// is a plain 404, and the viewer falls back to the full file.
func ServeClip(w http.ResponseWriter, r *http.Request, dir, name string) bool {
	m := clipName.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	game := m[1]
	kind, slot := ClipPOTG, 0
	if m[3] != "" {
		kind = ClipHighlight
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
	from, to, err := ClipWindow(txt, kind, slot)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	out, err := SliceRPL(full, from, to)
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
