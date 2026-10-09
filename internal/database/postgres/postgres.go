package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Params struct {
	fx.In
	Config Config
	Logger *zap.SugaredLogger
}

type Result struct {
	fx.Out
	PgxPool     lazy.Lazy[*pgxpool.Pool]
	SQLDB       lazy.Lazy[*sql.DB]
	PgxPoolWait *sync.WaitGroup `name:"pgx_pool_wait"`
	AppHook     fx.Hook         `group:"app_hooks"`
}

func New(p Params) (Result, error) {
	stopped := make(chan struct{})
	waitGroup := &sync.WaitGroup{}
	lazyPool := lazy.New(func() (*pgxpool.Pool, error) {
		ctx, cancel := context.WithCancel(context.Background())

		poolConfig, parseErr := pgxpool.ParseConfig(p.Config.CreateDSN())
		if parseErr != nil {
			cancel()
			return nil, parseErr
		}

		if p.Config.MaxConns > 0 {
			poolConfig.MaxConns = p.Config.MaxConns
		}

		if p.Config.MinConns > 0 {
			poolConfig.MinConns = p.Config.MinConns
		}

		pl, plErr := pgxpool.NewWithConfig(ctx, poolConfig)
		if plErr != nil {
			cancel()
			return nil, plErr
		}

		if pingErr := waitForPing(ctx, p.Logger, pl); pingErr != nil {
			cancel()
			return nil, pingErr
		}

		if scsErr := requireStandardConformingStrings(ctx, pl); scsErr != nil {
			cancel()
			pl.Close()

			return nil, scsErr
		}

		go func() {
			<-stopped
			// wait for services to be finished with the pool before closing
			waitGroup.Wait()
			cancel()
			pl.Close()
		}()

		return pl, nil
	})

	return Result{
		PgxPool: lazyPool,
		SQLDB: lazy.New(func() (*sql.DB, error) {
			pool, err := lazyPool.Get()
			if err != nil {
				return nil, err
			}

			return stdlib.OpenDBFromPool(pool), nil
		}),
		PgxPoolWait: waitGroup,
		AppHook: fx.Hook{
			OnStop: func(context.Context) error {
				close(stopped)
				return nil
			},
		},
	}, nil
}

// requireStandardConformingStrings refuses a database where backslash is an
// escape character in ordinary string literals.
//
// Search renders its own SQL to a string, inlining values with their quotes
// doubled, and executes the result (see docs/architecture/data.md). Doubling is
// only sound while standard_conforming_strings is on: with it off, a search
// term containing `\'` closes the literal and the rest of the term runs as SQL.
// PostgreSQL has defaulted it on since 9.1, so finding it off means someone set
// it - per database, per role, in the DSN's options, or in a pooler - and the
// one safe answer is to stop rather than serve an injectable search.
func requireStandardConformingStrings(ctx context.Context, pool *pgxpool.Pool) error {
	var value string
	if err := pool.QueryRow(ctx, "SHOW standard_conforming_strings").Scan(&value); err != nil {
		return fmt.Errorf("reading standard_conforming_strings: %w", err)
	}

	if value != "on" {
		return fmt.Errorf(
			"standard_conforming_strings is %q, and bitmagnet requires it on: "+
				"search is open to SQL injection without it - "+
				"check ALTER DATABASE/ALTER ROLE settings and the connection's options",
			value,
		)
	}

	return nil
}

func waitForPing(ctx context.Context, logger *zap.SugaredLogger, pool *pgxpool.Pool) error {
	i := 0

	var err error

	for {
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}

		err = pool.Ping(ctx)
		if err == nil {
			return nil
		}

		i++
		if i > 10 {
			break
		}

		select {
		case <-ctx.Done():
			break
		case <-time.After(time.Second):
			logger.Warnw("failed to ping database, retrying...", "error", err)
			break
		}
	}

	return fmt.Errorf("timed out waiting for ping: %w", err)
}
