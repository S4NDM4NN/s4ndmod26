// accountsctl is for the operator, who has the database: it makes the first admin and fixes things the site's API
// cannot.  It needs DATABASE_URL.
//
//	accountsctl grant <username> <permission>
//	accountsctl revoke <username> <permission>
//	accountsctl verify <username>      mark the email address as confirmed (until the mail step exists)
//	accountsctl show <username>
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"s4ndmod26/status-api/accounts"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "accountsctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: accountsctl grant|revoke <username> <permission> | verify|show <username>")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	s := accounts.New(pool)
	if err := s.Migrate(ctx); err != nil {
		return err
	}
	a, err := s.ByUsername(ctx, args[1])
	if err != nil {
		return err
	}
	switch args[0] {
	case "grant", "revoke":
		if len(args) != 3 {
			return fmt.Errorf("usage: accountsctl %s <username> <permission>", args[0])
		}
		if args[0] == "grant" {
			err = s.Grant(ctx, a.ID, args[2], 0)
		} else {
			err = s.Revoke(ctx, a.ID, args[2])
		}
		if err != nil {
			return err
		}
	case "verify":
		if err := s.MarkVerified(ctx, a.ID); err != nil {
			return err
		}
	case "show":
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	a, _ = s.ByID(ctx, a.ID)
	fmt.Printf("%s (id %d) %s verified=%v permissions=%v\n", a.Username, a.ID, a.Email, a.Verified, a.Permissions)
	return nil
}
