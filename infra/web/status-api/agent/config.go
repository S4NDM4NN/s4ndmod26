// Package agent is s4nd-agent, the program that runs inside the game server container: it prepares the mod
// folder, starts iowolfded and restarts it if it dies, polls its status, bridges browser players (WebSocket) to
// the server's UDP port, and serves the server's own files (replays, stats, its pk3) over HTTP.  The website
// reads servers through this API, which is what lets a server run anywhere (see
// ~/.claude/plans/i-want-to-start-serialized-lovelace.md, Phase 2).
package agent

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	StateDir   string // the agent's private files (identity key): <mod folder>/.agent unless AGENT_STATE_DIR is set
	CORSOrigin string // Access-Control-Allow-Origin on the API ("*")
	PublicURL  string // how players and the website reach this agent (https://host); reported in /v1/info

	GameVersion string // the image's game version (S4ND_GAME_VERSION, set at build time)

	// Registration with a site's server registry (s4ndmod.com): AGENT_REGISTRATION_URL is the site (https://s4ndmod.com).
	// The agent then sends signed claims with the server's public addresses (the first of AGENT_PUBLIC_NAMES for UDP,
	// wss://<AGENT_PUBLIC_URL host>/ws for browsers, or AGENT_PUBLIC_WSS) until the site accepts them.
	// AGENT_REGISTRATION_TOKEN is the one-time token from the host's account page; it is only needed for the first
	// registration, and can be taken out of the configuration afterwards.
	RegistrationURL   string
	RegistrationToken string
	PublicWSS         string

	// TicketKey is s4ndmod.com's Ed25519 public key (GET <site>/api/tickets/key; any base64 form in the environment,
	// kept here URL-safe so it survives the engine's command line), handed to the game server as s4nd_ticket_key so it
	// can verify join tickets.  Empty: players join without an identity.
	TicketKey string

	// LinkURL is the site's claim endpoint (e.g. https://s4ndmod.com/api/link/claim).  With it, `/link CODE` typed by an
	// identified player is sent on to the site (linkrelay.go).  Empty: players are told linking is not available.
	LinkURL string

	// AuthKey is a secret made up fresh at every start (not kept anywhere): the game server mixes it into the
	// challenges it sends players for the key identity (s4nd_auth_key), so nobody can predict one.
	AuthKey string
	// PublicNames are the addresses this server answers to (AGENT_PUBLIC_NAMES, comma separated, e.g.
	// "play.example.com:27960,203.0.113.5:27960"), handed on as s4nd_public_names.  A player's signature is made
	// for the address they connected to; with names set, a signature made for another server is turned down.
	PublicNames string
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
		BaseDir:           env("AGENT_BASE_DIR", "/rtcw"),
		Mod:               env("AGENT_MOD", "s4ndmod26"),
		RetailDir:         env("AGENT_RETAIL_DIR", "/rtcw/retail/main"),
		Binary:            env("AGENT_SERVER_BINARY", "/rtcw/iowolfded.x86_64"),
		OmnibotDir:        env("AGENT_OMNIBOT_DIR", "/rtcw/omni-bot"),
		ExtraArgs:         args,
		Supervise:         env("AGENT_SUPERVISE", "1") != "0",
		CfgTemplate:       os.Getenv("SERVER_CFG_TEMPLATE"),
		RconPassword:      os.Getenv("RCON_PASSWORD"),
		DLURL:             os.Getenv("SV_DLURL"),
		Pure:              os.Getenv("SV_PURE"),
		DevMap:            os.Getenv("SV_DEVMAP") == "1",
		HTTPAddr:          env("AGENT_HTTP_ADDR", ":27970"),
		StateDir:          os.Getenv("AGENT_STATE_DIR"),
		CORSOrigin:        env("AGENT_CORS_ORIGIN", "*"),
		PublicURL:         strings.TrimRight(os.Getenv("AGENT_PUBLIC_URL"), "/"),
		GameVersion:       os.Getenv("S4ND_GAME_VERSION"),
		RegistrationURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_REGISTRATION_URL")), "/"),
		RegistrationToken: strings.TrimSpace(os.Getenv("AGENT_REGISTRATION_TOKEN")),
		PublicWSS:         strings.TrimSpace(os.Getenv("AGENT_PUBLIC_WSS")),
		TicketKey:         strings.TrimSpace(os.Getenv("TICKET_PUBLIC_KEY")),
		LinkURL:           strings.TrimSpace(os.Getenv("AGENT_LINK_URL")),
		PublicNames:       strings.Join(strings.FieldsFunc(os.Getenv("AGENT_PUBLIC_NAMES"), func(r rune) bool { return r == ',' || r == ' ' }), ","),
	}
	c.DistDir = env("AGENT_DIST_DIR", filepath.Join(c.BaseDir, c.Mod+"-dist"))
	if c.StateDir == "" {
		// Inside the server folder, so one folder (one volume) holds the whole server and its id survives restarts.
		// A dot-folder: the website refuses dot-files while it still serves that folder (until Phase 3).
		c.StateDir = filepath.Join(c.ModDir(), ".agent")
	}
	port, err := strconv.Atoi(env("RTCW_PORT", "27960"))
	if err != nil || port <= 0 || port > 65535 {
		return c, fmt.Errorf("RTCW_PORT %q is not a port", os.Getenv("RTCW_PORT"))
	}
	c.GamePort = port
	var secret [16]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return c, fmt.Errorf("no randomness for the challenge key: %v", err)
	}
	c.AuthKey = hex.EncodeToString(secret[:])
	if c.TicketKey != "" {
		// a wrong key would silently turn every ticket down: say so now
		key, err := normalizeTicketKey(c.TicketKey)
		if err != nil {
			return c, err
		}
		c.TicketKey = key
	}
	if c.RegistrationURL != "" {
		if _, _, err := c.registrationAddrs(); err != nil {
			return c, err
		}
	}
	return c, nil
}

// normalizeTicketKey checks the site's Ed25519 public key (32 bytes, base64 in any of its usual forms) and returns it
// in the URL-safe alphabet without padding.  That form is what the game server is given: the engine splits its
// command line at every '+' that is not in quotes, so a standard base64 key containing '+' would reach the game
// server cut in two.
func normalizeTicketKey(s string) (string, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return base64.RawURLEncoding.EncodeToString(b), nil
		}
	}
	return "", fmt.Errorf("TICKET_PUBLIC_KEY must be the site's Ed25519 public key: 32 bytes as base64")
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
