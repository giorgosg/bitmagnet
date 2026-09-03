// Package fixturecmd serves a bitmagnet whose index already has content in it,
// for an external harness to drive.
//
// It exists because a browser suite cannot test anything past a sign-in without
// one: the alternative is a real password lying around, or tests mutating a live
// index. This starts from a clone of the btm-testdb seed template, mints a fresh
// bootstrap invitation, prints it, and drops the clone when it exits — so the
// harness registers its own throwaway administrator and nothing is left behind.
package fixturecmd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dao"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/bitmagnet-io/bitmagnet/internal/dev/fixtureserver"
	"github.com/gin-gonic/gin"
	"github.com/urfave/cli/v2"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Params struct {
	fx.In
	Lifecycle fx.Lifecycle
	Logger    *zap.SugaredLogger
}

type Result struct {
	fx.Out
	Command *cli.Command `group:"commands"`
}

// shutdownGrace bounds how long a request may keep the server alive once
// shutdown has begun. It is deliberately well inside fx's own StopTimeout,
// because the database drop still has to happen after it.
const shutdownGrace = 3 * time.Second

// command owns the one piece of state that outlives a request: the cloned
// database, which has to be dropped exactly once however the process ends.
type command struct {
	logger *zap.SugaredLogger

	mu      sync.Mutex
	cleanup func()
}

func New(p Params) (r Result, err error) {
	c := &command{logger: p.Logger.Named("fixture")}

	// Dropping the clone from an fx OnStop hook rather than a defer in the
	// action is not a style choice. fx.App.Run blocks on a signal channel of its
	// own: on SIGTERM it returns, main returns, and the process exits - racing
	// any cleanup the action goroutine is still doing, which is how a clone gets
	// left behind on the fixture instance. fx waits for OnStop, so this is the
	// one hook that is guaranteed to run before the process goes away.
	p.Lifecycle.Append(fx.Hook{
		OnStop: func(context.Context) error {
			c.runCleanup()

			return nil
		},
	})

	r.Command = &cli.Command{
		Name:  "fixture",
		Usage: "run a bitmagnet backed by the btm-testdb seed template",
		Subcommands: []*cli.Command{
			{
				Name:   "serve",
				Usage:  "serve a seeded bitmagnet and print its address and bootstrap invitation",
				Flags:  flags(),
				Action: c.serve,
			},
		},
	}

	return r, nil
}

// setCleanup records how to drop this run's clone.
func (c *command) setCleanup(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanup = fn
}

// runCleanup drops the clone, at most once, whichever path reaches it first.
func (c *command) runCleanup() {
	c.mu.Lock()
	fn := c.cleanup
	c.cleanup = nil
	c.mu.Unlock()

	if fn != nil {
		c.logger.Info("dropping the fixture database")
		fn()
	}
}

func flags() []cli.Flag {
	defaults := authconfig.NewDefaultConfig()

	return []cli.Flag{
		&cli.StringFlag{
			Name:  "address",
			Usage: "listen address; port 0 picks a free one, which is what parallel runs want",
			Value: "127.0.0.1:0",
		},
		&cli.StringFlag{
			Name:    "template-dsn",
			Usage:   "admin connection string for the instance holding the seed template",
			EnvVars: []string{dbtest.SeededDSNEnvVar},
		},
		&cli.BoolFlag{
			Name:  "anonymous-access",
			Usage: "grant the anon role every registered object action",
			Value: defaults.AnonymousAccess,
		},
		&cli.BoolFlag{
			Name:  "invitation-required",
			Usage: "require an invitation code to register",
			Value: defaults.InvitationRequired,
		},
		&cli.DurationFlag{
			Name:  "jwt-duration",
			Usage: "session token lifetime",
			Value: defaults.JWTDuration,
		},
		&cli.IntFlag{
			Name:  "login-requests-per-minute",
			Usage: "login throttle rate; set it low to provoke throttling deliberately",
			Value: defaults.LoginRequestsPerMinute,
		},
		&cli.IntFlag{
			Name:  "login-request-burst",
			Usage: "login throttle burst; set it to 1 to make the second attempt throttle",
			Value: defaults.LoginRequestBurst,
		},
	}
}

// announcement is the one line of JSON printed on stdout once the server is up.
//
// JSON rather than log lines so a harness can read it with a parser instead of a
// regex over output whose format is not a contract.
type announcement struct {
	Address            string `json:"address"`
	GraphQLEndpoint    string `json:"graphqlEndpoint"`
	InvitationCode     string `json:"invitationCode"`
	Database           string `json:"database"`
	AnonymousAccess    bool   `json:"anonymousAccess"`
	InvitationRequired bool   `json:"invitationRequired"`
}

func (c *command) serve(cliCtx *cli.Context) error {
	templateDSN := cliCtx.String("template-dsn")
	if templateDSN == "" {
		return fmt.Errorf(
			"no seed template instance given: pass --template-dsn or set %s",
			dbtest.SeededDSNEnvVar,
		)
	}

	// Release mode, and gin's own output to stderr. stdout carries exactly one
	// line -- the announcement -- because the harness reading it parses that
	// line rather than scraping logs, and gin's debug route dump would sit in
	// front of it.
	gin.SetMode(gin.ReleaseMode)

	gin.DefaultWriter = os.Stderr
	gin.DefaultErrorWriter = os.Stderr

	cfg, err := configFromFlags(cliCtx)
	if err != nil {
		return err
	}

	db, err := dbtest.OpenSeeded(cliCtx.Context, templateDSN)
	if err != nil {
		return err
	}

	// Registered before anything else can fail, so every path out of this
	// function drops the clone.
	c.setCleanup(db.Close)
	defer c.runCleanup()

	stack, err := fixtureserver.Build(fixtureserver.Options{
		Config:    cfg,
		Provider:  provider{query: db.Query},
		Logger:    c.logger,
		JWTSecret: cfg.JWTSecret,
	})
	if err != nil {
		return err
	}

	invitation, err := stack.UserService.CreateInitialInvitation(cliCtx.Context)
	if err != nil {
		return fmt.Errorf("minting the bootstrap invitation: %w", err)
	}

	address := cliCtx.String("address")

	var lc net.ListenConfig

	listener, err := lc.Listen(cliCtx.Context, "tcp", address)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", address, err)
	}

	base := "http://" + listener.Addr().String()
	if err := announce(os.Stdout, announcement{
		Address:            base,
		GraphQLEndpoint:    base + "/graphql",
		InvitationCode:     invitation.Code,
		Database:           db.Name,
		AnonymousAccess:    cfg.AnonymousAccess,
		InvitationRequired: cfg.InvitationRequired,
	}); err != nil {
		_ = listener.Close()

		return err
	}

	return c.run(cliCtx.Context, listener, stack.Engine)
}

// run serves until the context is cancelled or the process is stopped. The
// signal itself is fx's to catch -- see the OnStop hook in New -- so this waits
// on the listener and lets Shutdown be driven from there.
func (c *command) run(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler: handler,
		// The harness is local and trusted; this is here so a stuck client
		// cannot hold a header read open indefinitely.
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)

	go func() { serveErr <- server.Serve(listener) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		// Reported, not returned: dropping the database matters more than a
		// clean close, and returning here would name the wrong thing as the
		// failure.
		c.logger.Warnw("server did not shut down cleanly", "error", err)
	}

	return nil
}

// announce writes the one JSON line, so a harness reading stdout sees it before
// the server starts accepting. It takes the writer so a test can read back what
// a harness would have parsed.
func announce(w io.Writer, a announcement) error {
	encoded, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("encoding the announcement: %w", err)
	}

	if _, err := fmt.Fprintln(w, string(encoded)); err != nil {
		return fmt.Errorf("writing the announcement: %w", err)
	}

	return nil
}

func configFromFlags(ctx *cli.Context) (authconfig.Config, error) {
	cfg := authconfig.NewDefaultConfig()
	cfg.AnonymousAccess = ctx.Bool("anonymous-access")
	cfg.InvitationRequired = ctx.Bool("invitation-required")
	cfg.JWTDuration = ctx.Duration("jwt-duration")
	cfg.LoginRequestsPerMinute = ctx.Int("login-requests-per-minute")
	cfg.LoginRequestBurst = ctx.Int("login-request-burst")

	secret, err := randomSecret()
	if err != nil {
		return authconfig.Config{}, err
	}

	cfg.JWTSecret = secret

	return cfg, nil
}

// randomSecret signs this run's tokens. Per-process and never persisted: the
// database goes away when the command exits, so a token that outlived it would
// name a user that no longer exists.
func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating a JWT secret: %w", err)
	}

	return base64.RawStdEncoding.EncodeToString(buf), nil
}

// provider adapts the cloned database to the interface the services take.
type provider struct{ query *dao.Query }

func (p provider) Dao() (*dao.Query, error) { return p.query, nil }

func (p provider) DaoTransaction(fn func(tx *dao.Query) error) error {
	return p.query.Transaction(fn)
}
