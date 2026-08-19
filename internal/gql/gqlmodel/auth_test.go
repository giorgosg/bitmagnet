package gqlmodel

import (
	"database/sql"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/gqlmodel/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An omitted field, an explicit null, and a present value are three different
// things in GraphQL, and only the last should reach the service layer.
func TestOptionalUnwrapsOmittable(t *testing.T) {
	t.Parallel()

	value := "hello"

	present := graphql.OmittableOf(&value)
	got, ok := optional(present)
	assert.True(t, ok)
	assert.Equal(t, "hello", got)

	explicitNull := graphql.OmittableOf[*string](nil)
	got, ok = optional(explicitNull)
	assert.False(t, ok, "an explicit null must not be treated as a value")
	assert.Empty(t, got)

	var omitted graphql.Omittable[*string]
	got, ok = optional(omitted)
	assert.False(t, ok, "an omitted field must not be treated as a value")
	assert.Empty(t, got)
}

func TestPaginationParams(t *testing.T) {
	t.Parallel()

	limit, page, offset := paginationParams(graphql.Omittable[*gen.PaginationInput]{})
	assert.Equal(t, 0, limit)
	assert.Equal(t, 0, page)
	assert.Equal(t, 0, offset)

	l, p := 10, 2
	limit, page, offset = paginationParams(graphql.OmittableOf(&gen.PaginationInput{
		Limit: graphql.OmittableOf(&l),
		Page:  graphql.OmittableOf(&p),
	}))
	assert.Equal(t, 10, limit)
	assert.Equal(t, 2, page)
	assert.Equal(t, 0, offset, "an omitted offset defaults to zero rather than erroring")
}

// A null timestamp must become a nil pointer, not the zero time, which would be
// rendered as year 1 rather than null.
func TestNullTimePtr(t *testing.T) {
	t.Parallel()

	assert.Nil(t, NullTimePtr(sql.NullTime{}))

	now := time.Now()
	got := NullTimePtr(sql.NullTime{Time: now, Valid: true})
	require.NotNil(t, got)
	assert.Equal(t, now, *got)
}

func TestTimePtr(t *testing.T) {
	t.Parallel()

	assert.Nil(t, TimePtr(time.Time{}), "a zero expiry means no expiry, not year 1")

	now := time.Now()
	require.NotNil(t, TimePtr(now))
}

func TestRoleFromInfoCarriesPermissions(t *testing.T) {
	t.Parallel()

	objAct := rbac.NewObjectAction("torrent", "torrent", "query")
	info := rbac.RoleInfo{
		Role: rbac.RoleAdmin,
		Core: true,
		Permissions: []rbac.Permission{
			rbac.NewPermission(rbac.SubjectRole{Role: rbac.RoleAdmin}, objAct),
		},
	}

	got := RoleFromInfo(info)

	assert.Equal(t, "admin", got.Name)
	assert.True(t, got.Core)
	require.Len(t, got.Permissions, 1)
	assert.Equal(t, gen.AuthSubjectType("role"), got.Permissions[0].Subject.Type)
	assert.Equal(t, "admin", got.Permissions[0].Subject.Name)
	assert.Equal(t, "torrent", got.Permissions[0].ObjectAction.Namespace)
}

func TestObjectActionsRoundTrip(t *testing.T) {
	t.Parallel()

	original := []rbac.ObjectAction{
		rbac.NewObjectAction("a", "b", "c"),
		rbac.NewObjectAction("d", "e", "f"),
	}

	asGql := ObjectActionsToGql(original)
	asInput := make([]gen.AuthObjectActionInput, 0, len(asGql))

	for _, g := range asGql {
		asInput = append(asInput, gen.AuthObjectActionInput(g))
	}

	assert.Equal(t, original, ObjectActionsFromGql(asInput))
}
