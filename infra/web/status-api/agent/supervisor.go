package agent

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ServerArgs are iowolfded's arguments: the same as the old entrypoint script's, then the operator's own.  serverID is
// this server's id (Identity.ID): the game server reports it as s4nd_server_id (a server info variable, so it shows in
// getstatus) and only accepts join tickets made for it.
func ServerArgs(c Config, cfg, serverID string) []string {
	args := []string{
		"+set", "dedicated", "2",
		"+set", "fs_basepath", c.BaseDir,
		"+set", "fs_homepath", c.BaseDir,
		"+set", "fs_game", c.Mod,
		"+set", "omnibot_path", c.OmnibotDir,
	}
	if c.GamePort != 27960 {
		args = append(args, "+set", "net_port", strconv.Itoa(c.GamePort))
	}
	if serverID != "" {
		args = append(args, "+set", "s4nd_server_id", serverID)
	}
	if c.TicketKey != "" {
		args = append(args, "+set", "s4nd_ticket_key", c.TicketKey)
	}
	args = append(args, "+exec", cfg)
	return append(args, c.ExtraArgs...)
}

// Supervisor runs the server process and starts it again when it exits, waiting longer after each quick crash
// (Backoff doubles from MinBackoff up to MaxBackoff; a run that lasted StableAfter resets it).
type Supervisor struct {
	Path        string
	Args        []string
	Dir         string
	MinBackoff  time.Duration
	MaxBackoff  time.Duration
	StableAfter time.Duration
	StopTimeout time.Duration // after asking the server to stop, kill it if it is still running

	mu       sync.Mutex
	starts   int
	running  bool
	lastExit string
}

// SupervisorState is what /v1/info reports about the process.
type SupervisorState struct {
	Running  bool   `json:"running"`
	Starts   int    `json:"starts"`
	LastExit string `json:"last_exit,omitempty"`
}

func (s *Supervisor) State() SupervisorState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SupervisorState{Running: s.running, Starts: s.starts, LastExit: s.lastExit}
}

// Run keeps the server running until ctx is cancelled, then stops it (SIGTERM, SIGKILL after StopTimeout).
func (s *Supervisor) Run(ctx context.Context) {
	backoff := s.MinBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		ran := time.Since(started)
		log.Printf("server exited after %s: %v", ran.Round(time.Second), errText(err))
		if ran >= s.StableAfter {
			backoff = s.MinBackoff
		}
		log.Printf("restarting the server in %s", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > s.MaxBackoff {
			backoff = s.MaxBackoff
		}
	}
}

func (s *Supervisor) runOnce(ctx context.Context) error {
	cmd := exec.Command(s.Path, s.Args...)
	cmd.Dir = s.Dir
	cmd.Stdin = nil // dedicated server console: nothing to read, as under Docker without -i
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Its own process group, so stopping it also stops anything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		s.setExit(false, err)
		return err
	}
	s.mu.Lock()
	s.starts++
	s.running = true
	s.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		s.setExit(false, err)
		return err
	case <-ctx.Done():
		pgid := -cmd.Process.Pid
		syscall.Kill(pgid, syscall.SIGTERM)
		select {
		case err := <-done:
			syscall.Kill(pgid, syscall.SIGKILL) // anything left behind in the group
			s.setExit(false, err)
			return err
		case <-time.After(s.StopTimeout):
			syscall.Kill(pgid, syscall.SIGKILL)
			err := <-done
			s.setExit(false, err)
			return err
		}
	}
}

func (s *Supervisor) setExit(running bool, err error) {
	s.mu.Lock()
	s.running = running
	s.lastExit = errText(err)
	s.mu.Unlock()
}

func errText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}
