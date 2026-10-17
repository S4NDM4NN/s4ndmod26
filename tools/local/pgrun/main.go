// pgrun starts a throwaway Postgres (downloaded once into ~/.cache/s4ndmod26-pg), runs the given command with
// STATS_TEST_DSN pointing at it, then stops it.  Local tooling only: it is its own module so the status API does not
// depend on it.  Usage: pgrun <command> [args...]
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pgrun <command> [args...]")
		os.Exit(2)
	}
	home, _ := os.UserHomeDir()
	cache := filepath.Join(home, ".cache", "s4ndmod26-pg")
	work, err := os.MkdirTemp("", "pgrun")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(work)

	const port = 54329
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(port).
		BinariesPath(filepath.Join(cache, "bin")).
		CachePath(filepath.Join(cache, "dl")).
		RuntimePath(filepath.Join(work, "rt")).
		DataPath(filepath.Join(work, "data")).
		Logger(nil))
	if err := pg.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "starting postgres:", err)
		os.Exit(1)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("STATS_TEST_DSN=postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	runErr := cmd.Run()
	pg.Stop()
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
