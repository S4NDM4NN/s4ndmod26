package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	if c.AuthKey != "" {
		args = append(args, "+set", "s4nd_auth_key", c.AuthKey)
	}
	if c.PublicNames != "" {
		args = append(args, "+set", "s4nd_public_names", c.PublicNames)
	}
	if c.LinkURL != "" {
		args = append(args, "+set", "s4nd_link", "1")
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
	stdin    io.WriteCloser // the running server's console input; nil when it is not running
	consMu   sync.Mutex     // one Console call at a time
	capMu    sync.Mutex
	capture  *consoleCapture
	consSeq  int
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
	// The agent talks to the server's console through a pipe: commands go in on stdin, and everything the server
	// prints still reaches our stdout (the container log) while Console watches it for the answer.  The engine
	// reads a piped stdin line by line (it only turns the tty console off).
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.setExit(false, err)
		return err
	}
	out := &consoleTee{s: s}
	cmd.Stdout = out
	cmd.Stderr = out
	// Its own process group, so stopping it also stops anything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		s.setExit(false, err)
		return err
	}
	s.mu.Lock()
	s.starts++
	s.running = true
	s.stdin = stdin
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
	if !running {
		s.stdin = nil
	}
	s.lastExit = errText(err)
	s.mu.Unlock()
}

// consoleCapture collects the lines printed between two marker lines.
type consoleCapture struct {
	begin, end string
	on         bool
	buf        bytes.Buffer
	done       chan struct{}
}

// consoleTee is the server's stdout/stderr: it passes everything to our stdout and feeds complete lines to a
// Console call that is waiting for its answer.
type consoleTee struct {
	s    *Supervisor
	part []byte
}

func (t *consoleTee) Write(p []byte) (int, error) {
	os.Stdout.Write(p)
	t.part = append(t.part, p...)
	for {
		i := bytes.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(t.part[:i], "\r"))
		t.part = t.part[i+1:]
		t.s.feed(line)
	}
	if len(t.part) > 1<<16 { // a line that never ends
		t.part = nil
	}
	return len(p), nil
}

func (s *Supervisor) feed(line string) {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	c := s.capture
	if c == nil {
		return
	}
	switch {
	case line == c.end:
		s.capture = nil
		close(c.done)
	case c.on:
		c.buf.WriteString(line)
		c.buf.WriteByte('\n')
	case line == c.begin:
		c.on = true
	}
}

// Console runs one command on the server's console and returns what it printed (read from the server's own
// stdout, between two `echo` marker lines, so nothing else needs to be configured).
// It fails when the server is not running or does not answer within timeout.
func (s *Supervisor) Console(command string, timeout time.Duration) (string, error) {
	s.consMu.Lock()
	defer s.consMu.Unlock()
	s.mu.Lock()
	in := s.stdin
	s.consSeq++
	n := s.consSeq
	s.mu.Unlock()
	if in == nil {
		return "", fmt.Errorf("the server is not running")
	}
	c := &consoleCapture{begin: fmt.Sprintf("s4nd-console-%d-begin", n), end: fmt.Sprintf("s4nd-console-%d-end", n), done: make(chan struct{})}
	s.capMu.Lock()
	s.capture = c
	s.capMu.Unlock()
	defer func() {
		s.capMu.Lock()
		if s.capture == c {
			s.capture = nil
		}
		s.capMu.Unlock()
	}()
	if _, err := io.WriteString(in, "echo "+c.begin+"\n"+command+"\necho "+c.end+"\n"); err != nil {
		return "", err
	}
	select {
	case <-c.done:
		s.capMu.Lock()
		defer s.capMu.Unlock()
		return c.buf.String(), nil
	case <-time.After(timeout):
		return "", fmt.Errorf("no answer from the server console within %s", timeout)
	}
}

func errText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}
