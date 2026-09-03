package dbtest

// The seeded path for callers that have no *testing.T: the fixture server under
// internal/dev needs the same clone the seeded tests get, and duplicating the
// provisioning would be a second thing to keep in step with btm-testdb.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNoFixtures reports that this host has no seed template to clone — the
// fixture instance is reachable but carries none, or none matching the
// migrations in this tree.
//
// Tests skip on this; a command asked for a seeded database explicitly, so it
// should say what is missing and stop. Separating it from the other errors is
// what lets each caller do the right thing.
var ErrNoFixtures = errors.New("seed fixtures unavailable")

// SeededDatabase is a clone of the seed template owned by a caller that manages
// its own lifetime, rather than hanging it on a test's cleanup.
type SeededDatabase struct {
	*DB

	adminDSN string
	sqlDB    *sql.DB
}

// Close drops the clone. It is safe to call once; a second call does nothing
// useful, and the underlying DROP is IF EXISTS.
func (s *SeededDatabase) Close() {
	s.drop(s.adminDSN, s.sqlDB)
}

// OpenSeeded clones the seed template on the instance adminDSN names, exactly as
// [NewSeeded] does, and hands back a handle the caller closes itself.
//
// It returns an error wrapping [ErrNoFixtures] when the instance carries no
// usable template, so a caller can tell "the fixtures were never built here"
// from "the fixture setup is broken".
func OpenSeeded(ctx context.Context, adminDSN string) (*SeededDatabase, error) {
	db, sqlDB, err := provisionSeeded(ctx, adminDSN)
	if err != nil {
		var skip skipError
		if errors.As(err, &skip) {
			return nil, fmt.Errorf("%w: %s", ErrNoFixtures, skip.Error())
		}

		return nil, err
	}

	return &SeededDatabase{DB: db, adminDSN: adminDSN, sqlDB: sqlDB}, nil
}
