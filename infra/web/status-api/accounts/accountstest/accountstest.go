// Package accountstest gives tests an accounts.Service on a throwaway Postgres schema.  Set STATS_TEST_DSN
// (tools/local/accounts_test.sh starts a database).
package accountstest

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"s4ndmod26/status-api/accounts"
)

// New returns a migrated service in its own schema, dropped when the test ends.  The test is skipped without a database.
func New(t testing.TB) *accounts.Service {
	t.Helper()
	dsn := os.Getenv("STATS_TEST_DSN")
	if dsn == "" {
		t.Skip("STATS_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("a_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := accounts.New(pool)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
