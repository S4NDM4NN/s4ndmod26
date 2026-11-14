// Package replayviews serves the views built from a server's replay folder: where players went (paths), the fights of a game and each fight in 3D,
// and the live view of the running game.  It is a package of its own so the site (for its own server) and the server agent (for any
// registered server) serve the same thing, straight from the files, with no database.
package replayviews

import (
	"os"
	"regexp"
)

// matchNameRe is what a game's name may look like.
var matchNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// Exported so each program can say which folder it serves, and register the routes the same way.

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
