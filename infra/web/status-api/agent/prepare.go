package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Prepare readies the mod folder before the server starts, as infra/server-entrypoint.sh did:
//   - the replays folder exists;
//   - the image's qagame and pk3 replace what is in the (volume-mounted) mod folder, so a new image always runs
//     its own build without touching operator files such as server.cfg; older pk3s are removed, because with
//     sv_pure on two pk3s holding a cgame/ui would both be searched;
//   - latest.txt names the current pk3;
//   - retail paks mounted at RetailDir are linked into main/ (links to paks no longer mounted are removed).
//
// It returns the name of the config file the server should execute.
func Prepare(c Config) (cfg string, err error) {
	mod := c.ModDir()
	if err := os.MkdirAll(c.ReplayDir(), 0o755); err != nil {
		return "", err
	}
	if err := os.Chmod(c.ReplayDir(), 0o755); err != nil {
		return "", err
	}

	if _, err := os.Stat(c.DistDir); err == nil {
		if err := copyFile(filepath.Join(c.DistDir, "qagame.mp.x86_64.so"), filepath.Join(mod, "qagame.mp.x86_64.so")); err != nil {
			return "", err
		}
		pk3s, _ := filepath.Glob(filepath.Join(c.DistDir, c.Mod+"_*.pk3"))
		if len(pk3s) != 1 {
			return "", fmt.Errorf("want exactly one %s_*.pk3 in %s, found %d", c.Mod, c.DistDir, len(pk3s))
		}
		old, _ := filepath.Glob(filepath.Join(mod, c.Mod+"_*.pk3"))
		old = append(old, filepath.Join(mod, c.Mod+".pk3"))
		for _, f := range old {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
		name := filepath.Base(pk3s[0])
		if err := copyFile(pk3s[0], filepath.Join(mod, name)); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(mod, "latest.txt"), []byte(name+"\n"), 0o644); err != nil {
			return "", err
		}
	}

	if err := linkRetail(c); err != nil {
		return "", err
	}

	if c.CfgTemplate == "" {
		if err := writeStarterConfig(c); err != nil {
			return "", err
		}
		return "server.cfg", nil
	}
	tmpl, err := os.ReadFile(c.CfgTemplate)
	if err != nil {
		return "", fmt.Errorf("server config template: %w", err)
	}
	// Written to main/ inside the container, never to the mod folder: it holds the rcon password, and the mod
	// folder is shared with the website (which serves it under /downloads/) until Phase 3.
	if err := os.MkdirAll(c.MainDir(), 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(c.MainDir(), c.GeneratedCfg())
	if err := os.WriteFile(out, []byte(FillConfig(string(tmpl), c)), 0o600); err != nil {
		return "", err
	}
	return c.GeneratedCfg(), nil
}

// StarterConfig is the image's default config for a new self-hosted server (infra/server/server.cfg.default).
const StarterConfig = "server.cfg.default"

// writeStarterConfig gives a server folder with no server.cfg one to start from: the image's default with a random
// rcon password (or RCON_PASSWORD) and sv_dlURL set to SV_DLURL, else to this agent's /dl when AGENT_PUBLIC_URL is
// set, else empty.  An existing server.cfg is never touched.
func writeStarterConfig(c Config) error {
	path := filepath.Join(c.ModDir(), "server.cfg")
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return err
	}
	tmpl, err := os.ReadFile(filepath.Join(c.DistDir, StarterConfig))
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	rcon := c.RconPassword
	if rcon == "" {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		rcon = hex.EncodeToString(b)
	}
	dl := c.DLURL
	if dl == "" && c.PublicURL != "" {
		dl = c.PublicURL + "/dl"
	}
	cfg := strings.NewReplacer("RCON_PASSWORD_PLACEHOLDER", rcon, "SV_DLURL_PLACEHOLDER", dl).Replace(string(tmpl))
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		return err
	}
	log.Printf("wrote a starter %s (with a random rcon password unless RCON_PASSWORD was set); edit it and restart", path)
	return nil
}

var mapLine = regexp.MustCompile(`(?m)^([ \t]*)map `)

// FillConfig replaces the template's placeholders (the same ones the init container's sed replaced).  A setting
// left empty keeps its placeholder, so a missing value shows up in the config instead of silently blanking it.
func FillConfig(tmpl string, c Config) string {
	r := []string{}
	if c.RconPassword != "" {
		r = append(r, "RCON_PASSWORD_PLACEHOLDER", c.RconPassword)
	}
	if c.DLURL != "" {
		r = append(r, "SV_DLURL_PLACEHOLDER", c.DLURL)
	}
	if c.Pure != "" {
		r = append(r, "SV_PURE_PLACEHOLDER", c.Pure)
	}
	s := strings.NewReplacer(r...).Replace(tmpl)
	if c.DevMap {
		s = mapLine.ReplaceAllString(s, "${1}devmap ")
	}
	return s
}

func linkRetail(c Config) error {
	main := c.MainDir()
	entries, err := os.ReadDir(main)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	retailRoot := filepath.Dir(c.RetailDir) // links point into it (/rtcw/retail/...)
	for _, e := range entries {
		p := filepath.Join(main, e.Name())
		if e.Type()&os.ModeSymlink == 0 || !strings.HasSuffix(e.Name(), ".pk3") {
			continue
		}
		if target, err := os.Readlink(p); err == nil && strings.HasPrefix(target, retailRoot+"/") {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	paks, _ := filepath.Glob(filepath.Join(c.RetailDir, "*.pk3"))
	for _, f := range paks {
		if st, err := os.Stat(f); err != nil || !st.Mode().IsRegular() {
			continue
		}
		link := filepath.Join(main, filepath.Base(f))
		os.Remove(link) // ln -sf
		if err := os.Symlink(f, link); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".agent-tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
