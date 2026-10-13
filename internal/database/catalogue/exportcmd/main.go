// Command exportcmd streams a portable catalogue SQL dump. PostgreSQL connection
// settings follow libpq's PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE environment.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bitmagnet-io/bitmagnet/internal/database/catalogue"
	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	config, err := pgx.ParseConfig("")
	if err != nil {
		return err
	}

	if os.Getenv("PGDATABASE") == "" {
		config.Database = "bitmagnet"
	}

	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close(ctx) }()

	return catalogue.Export(ctx, conn, os.Stdout)
}
