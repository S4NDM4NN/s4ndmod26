// Package agent is s4nd-agent, the program that runs inside the game server container: it prepares the mod
// folder, starts iowolfded and restarts it if it dies, polls its status, bridges browser players (WebSocket) to
// the server's UDP port, and serves the server's own files (replays, stats, its pk3) over HTTP.  The website
// reads servers through this API, which is what lets a server run anywhere (see
// ~/.claude/plans/i-want-to-start-serialized-lovelace.md, Phase 2).
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is read from the environment (FromEnv); the defaults are the paths of the s4ndmod26 server image.
type Config struct {
	BaseDir    string // fs_basepath and fs_homepath (/rtcw)
	Mod        string // fs_game (s4ndmod26)
	DistDir    string // the image's copy of qagame + pk3, copied into the mod folder at start (/rtcw/s4ndmod26-dist)
	RetailDir  string // operator-mounted retail paks, linked into main/ when present (/rtcw/retail/main)
	Binary     string // the dedicated server (/rtcw/iowolfded.x86_64)
	OmnibotDir string // omnibot_path (/rtcw/omni-bot)
	GamePort   int    // net_port (27960)
	ExtraArgs  []string

	// Supervise false runs only the HTTP side (for a server started some other way, and for tests).
	Supervise bool

	// Server config.  With CfgTemplate set, the agent fills in its placeholders from these and executes the
	// result instead of server.cfg (what the busybox init container's sed did).  Without it, server.cfg runs as is.
	CfgTemplate  string
	RconPassword string
	DLURL        string // sv_dlURL base; "/downloads" is appended as before
	Pure         string // sv_pure ("" leaves the template's placeholder untouched)
	DevMap       bool   // start the map with devmap (cheats on), as the PR previews do

	HTTPAddr   string // the agent's own listener: API, /ws bridge and /dl downloads (:27970)
	StateDir   string // the agent's private files (identity key).  Must not be a folder the website serves.
	CORSOrigin string // Access-Control-Allow-Origin on the API ("*")
	PublicURL  string // how players and the website reach this agent (https://host); reported in /v1/info

	GameVersion string // the image's game version (S4ND_GAME_VERSION, set at build time)

	RegistryEnabled bool   // send signed heartbeats to RegistryURL (off until s4ndmod.com has a registry)
	RegistryURL     string // e.g. https://s4ndmod.com/api/servers/heartbeat
}

func (c Config) ModDir() string       { return filepath.Join(c.BaseDir, c.Mod) }
func (c Config) ReplayDir() string    { return filepath.Join(c.ModDir(), "replays") }
func (c Config) StatsDir() string     { return filepath.Join(c.ModDir(), "stats") }
func (c Config) GameAddr() string     { return fmt.Sprintf("127.0.0.1:%d", c.GamePort) }
func (c Config) MainDir() string      { return filepath.Join(c.BaseDir, "main") }
func (c Config) GeneratedCfg() string { return "agent_server.cfg" }

// FromEnv reads the configuration.  args are passed on to iowolfded after the agent's own arguments (like "$@" in
// the old entrypoint script).
func FromEnv(args []string) (Config, error) {
	c := Config{
		BaseDir:         env("AGENT_BASE_DIR", "/rtcw"),
		Mod:             env("AGENT_MOD", "s4ndmod26"),
		RetailDir:       env("AGENT_RETAIL_DIR", "/rtcw/retail/main"),
		Binary:          env("AGENT_SERVER_BINARY", "/rtcw/iowolfded.x86_64"),
		OmnibotDir:      env("AGENT_OMNIBOT_DIR", "/rtcw/omni-bot"),
		ExtraArgs:       args,
		Supervise:       env("AGENT_SUPERVISE", "1") != "0",
		CfgTemplate:     os.Getenv("SERVER_CFG_TEMPLATE"),
		RconPassword:    os.Getenv("RCON_PASSWORD"),
		DLURL:           os.Getenv("SV_DLURL"),
		Pure:            os.Getenv("SV_PURE"),
		DevMap:          os.Getenv("SV_DEVMAP") == "1",
		HTTPAddr:        env("AGENT_HTTP_ADDR", ":27970"),
		StateDir:        env("AGENT_STATE_DIR", "/rtcw/state"),
		CORSOrigin:      env("AGENT_CORS_ORIGIN", "*"),
		PublicURL:       strings.TrimRight(os.Getenv("AGENT_PUBLIC_URL"), "/"),
		GameVersion:     os.Getenv("S4ND_GAME_VERSION"),
		RegistryEnabled: os.Getenv("REGISTRY_ENABLED") == "1",
		RegistryURL:     os.Getenv("REGISTRY_URL"),
	}
	c.DistDir = env("AGENT_DIST_DIR", filepath.Join(c.BaseDir, c.Mod+"-dist"))
	port, err := strconv.Atoi(env("RTCW_PORT", "27960"))
	if err != nil || port <= 0 || port > 65535 {
		return c, fmt.Errorf("RTCW_PORT %q is not a port", os.Getenv("RTCW_PORT"))
	}
	c.GamePort = port
	if c.RegistryEnabled && c.RegistryURL == "" {
		return c, fmt.Errorf("REGISTRY_ENABLED=1 needs REGISTRY_URL")
	}
	return c, nil
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
