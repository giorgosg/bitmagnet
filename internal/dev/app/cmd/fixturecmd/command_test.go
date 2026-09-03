package fixturecmd

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/authconfig"
	"github.com/bitmagnet-io/bitmagnet/internal/database/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

// newContext parses argv against the command's real flag set, so these tests
// exercise the flags a caller actually types rather than a hand-built struct.
//
// The context it carries is the test's, so a test that reaches real work is
// bounded by the test's own deadline instead of running until the suite is
// killed.
func newContext(t *testing.T, argv ...string) *cli.Context {
	t.Helper()

	set := flag.NewFlagSet("serve", flag.ContinueOnError)
	for _, f := range flags() {
		require.NoError(t, f.Apply(set))
	}

	require.NoError(t, set.Parse(argv))

	ctx := cli.NewContext(cli.NewApp(), set, nil)
	ctx.Context = t.Context()

	return ctx
}

func TestConfigFromFlagsDefaultsToTheAuthDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := configFromFlags(newContext(t))
	require.NoError(t, err)

	defaults := authconfig.NewDefaultConfig()
	assert.Equal(t, defaults.AnonymousAccess, cfg.AnonymousAccess)
	assert.Equal(t, defaults.InvitationRequired, cfg.InvitationRequired)
	assert.Equal(t, defaults.JWTDuration, cfg.JWTDuration)
	assert.Equal(t, defaults.LoginRequestsPerMinute, cfg.LoginRequestsPerMinute)
	assert.Equal(t, defaults.LoginRequestBurst, cfg.LoginRequestBurst)
}

// These five are the settings the ticket names, because they are the ones the
// browser workflows have to vary. A flag that silently does not reach the config
// would leave a workflow untestable while looking supported.
func TestConfigFromFlagsCarriesEverySetting(t *testing.T) {
	t.Parallel()

	cfg, err := configFromFlags(newContext(t,
		"--anonymous-access=false",
		"--invitation-required=false",
		"--jwt-duration=90s",
		"--login-requests-per-minute=2",
		"--login-request-burst=1",
	))
	require.NoError(t, err)

	assert.False(t, cfg.AnonymousAccess)
	assert.False(t, cfg.InvitationRequired)
	assert.Equal(t, 90*time.Second, cfg.JWTDuration)
	assert.Equal(t, 2, cfg.LoginRequestsPerMinute)
	assert.Equal(t, 1, cfg.LoginRequestBurst)
}

// The database goes away when the command exits, so a token that outlived the
// process would name a user that no longer exists. A per-run secret is what
// makes that impossible.
func TestConfigFromFlagsGeneratesAFreshSecret(t *testing.T) {
	t.Parallel()

	first, err := configFromFlags(newContext(t))
	require.NoError(t, err)

	second, err := configFromFlags(newContext(t))
	require.NoError(t, err)

	assert.NotEmpty(t, first.JWTSecret)
	assert.NotEqual(t, first.JWTSecret, second.JWTSecret)
}

// The contract with the harness: one line, parseable, carrying what it needs to
// drive the instance. Anything else on stdout breaks it, which is why gin is put
// in release mode and pointed at stderr before the server starts.
func TestAnnounceWritesOneParseableLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	require.NoError(t, announce(&buf, announcement{
		Address:            "http://127.0.0.1:41000",
		GraphQLEndpoint:    "http://127.0.0.1:41000/graphql",
		InvitationCode:     "abc123",
		Database:           "bitmagnet_test_1_2",
		AnonymousAccess:    false,
		InvitationRequired: true,
	}))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 1, "a harness reads one line; more than one is a broken contract")

	var decoded announcement
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &decoded))

	assert.Equal(t, "http://127.0.0.1:41000/graphql", decoded.GraphQLEndpoint)
	assert.Equal(t, "abc123", decoded.InvitationCode)
	assert.Equal(t, "bitmagnet_test_1_2", decoded.Database)
	assert.False(t, decoded.AnonymousAccess)
	assert.True(t, decoded.InvitationRequired)
}

// Without a template there is nothing to serve, and the command should say which
// variable is missing rather than failing somewhere further in.
//
// Not parallel, and the environment is cleared first, because --template-dsn
// reads TEST_POSTGRES_TEMPLATE_DSN: with that set, this test skipped its early
// return, cloned a ~1GB database and then served until the suite was killed.
// A test that can accidentally start a server is a test that can hang CI.
func TestServeRefusesWithoutATemplateDSN(t *testing.T) {
	t.Setenv(dbtest.SeededDSNEnvVar, "")
	require.NoError(t, os.Unsetenv(dbtest.SeededDSNEnvVar))

	c := &command{logger: testLogger(t)}

	err := c.serve(newContext(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), dbtest.SeededDSNEnvVar)
}

// runCleanup drops the clone at most once, however many paths reach it: the
// action's defer and fx's OnStop hook both call it, and a double DROP would
// report an error for work that already succeeded.
func TestRunCleanupIsIdempotent(t *testing.T) {
	t.Parallel()

	calls := 0
	c := &command{logger: testLogger(t)}
	c.setCleanup(func() { calls++ })

	c.runCleanup()
	c.runCleanup()

	assert.Equal(t, 1, calls)
}

func TestRunCleanupWithoutADatabaseDoesNothing(t *testing.T) {
	t.Parallel()

	c := &command{logger: testLogger(t)}

	assert.NotPanics(t, c.runCleanup)
}

func testLogger(t *testing.T) *zap.SugaredLogger {
	t.Helper()

	return zap.NewNop().Sugar()
}
