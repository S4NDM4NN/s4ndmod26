// s4nd-agent is the game server container's entrypoint: it prepares the mod folder, runs iowolfded (restarting
// it if it exits), and serves the server's HTTP API, browser bridge and downloads.  Settings are environment
// variables; see package agent (agent/config.go).  Arguments are passed on to iowolfded.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"s4ndmod26/status-api/agent"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("s4nd-agent: ")

	cfg, err := agent.FromEnv(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	id, err := agent.LoadIdentity(cfg.StateDir)
	if err != nil {
		log.Fatal(err)
	}
	if id.Ephemeral {
		log.Printf("server id %s (could not save it in %s: it changes on every start)", id.ID, cfg.StateDir)
	} else {
		log.Printf("server id %s", id.ID)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var sup *agent.Supervisor
	supDone := make(chan struct{})
	if cfg.Supervise {
		serverCfg, err := agent.Prepare(cfg)
		if err != nil {
			log.Fatalf("preparing the mod folder: %v", err)
		}
		sup = &agent.Supervisor{
			Path:        cfg.Binary,
			Args:        agent.ServerArgs(cfg, serverCfg, id.ID),
			Dir:         cfg.BaseDir,
			MinBackoff:  2 * time.Second,
			MaxBackoff:  time.Minute,
			StableAfter: 5 * time.Minute,
			StopTimeout: 10 * time.Second,
		}
		go func() {
			defer close(supDone)
			sup.Run(ctx)
		}()
	} else {
		close(supDone)
		log.Printf("AGENT_SUPERVISE=0: not starting the server; expecting it on %s", cfg.GameAddr())
	}

	a := agent.New(cfg, id, sup)
	go a.RunStatus(ctx)

	if cfg.RegistryEnabled {
		log.Printf("registry: heartbeats to %s", cfg.RegistryURL)
		go agent.RunRegistry(ctx, cfg.RegistryURL, time.Minute, id, a.Info)
	}

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		// The game must not go down with the HTTP side (on a hostNetwork pod the port is the node's, and could be
		// taken): log it and keep the server running.
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP API not available: %v (the game server keeps running)", err)
		}
	}()

	<-ctx.Done()
	log.Printf("stopping")
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shut) // WebSocket players are hijacked connections; they end when the server process does
	<-supDone
}
