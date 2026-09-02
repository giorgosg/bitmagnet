package api_key_test

import (
	"testing"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/api_key"
	api_key_mocks "github.com/bitmagnet-io/bitmagnet/internal/auth/api_key/mocks"
	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// registeredObjectAction stands in for the object actions the schema and the
// HTTP endpoints register. A key may name these and nothing else.
var registeredObjectAction = rbac.NewObjectAction("test", "foo", "bar")

type testHarness struct {
	repository *api_key_mocks.Repository
	service    api_key.Service
}

func newTestHarness(t *testing.T) testHarness {
	t.Helper()

	repository := api_key_mocks.NewRepository(t)

	return testHarness{
		repository: repository,
		service: api_key.NewService(repository, func() []rbac.ObjectAction {
			return []rbac.ObjectAction{registeredObjectAction}
		}),
	}
}

func TestService(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	permissions := []rbac.ObjectAction{registeredObjectAction}

	h.repository.EXPECT().
		Create(
			t.Context(),
			1,
			"test",
			mock.IsType([]byte{}),
			permissions,
			mock.IsType(time.Now()),
		).
		Return(2, nil).
		Once()

	result, err := h.service.Create(t.Context(), api_key.CreateRequest{
		UserID:      1,
		Name:        "test",
		Permissions: permissions,
		Expiry:      time.Hour * 24,
	})

	require.NoError(t, err)

	assert.Equal(t, 2, result.ID)
	assert.Len(t, result.APIKey, 22)
	assert.WithinDuration(t, time.Now().Add(time.Hour*24), result.ExpiresAt, time.Second)
}

// api_key_permissions has no foreign key to a registry of object actions,
// because no such table exists. So whatever createAPIKey is handed is stored.
//
// A typo therefore produced a key that grants nothing, with no error at
// creation and no way to tell from the API that it is dead. listObjectActions
// already serves the registry; this checks against it.
func TestCreateRejectsAnUnregisteredObjectAction(t *testing.T) {
	t.Parallel()

	for _, unregistered := range []rbac.ObjectAction{
		{Namespace: "nonsense", Object: "nonsense", Action: "nonsense"},
		// One component wrong is the typo that actually happens.
		{Namespace: "test", Object: "foo", Action: "barr"},
		{},
	} {
		h := newTestHarness(t)

		_, err := h.service.Create(t.Context(), api_key.CreateRequest{
			UserID:      1,
			Name:        "typo",
			Permissions: []rbac.ObjectAction{unregistered},
		})

		require.Error(t, err, "%v must be refused", unregistered)
		assert.ErrorIs(t, err, api_key.ErrPermissionInvalid)
	}
}

// A wildcard stored in a key's own selection is matched by globMatch at
// enforcement, so the key's half of the decision degenerates to "anything".
// That is contained only because the other half still requires the owning
// User's Role — a containment worth not relying on.
func TestCreateRejectsWildcardObjectActions(t *testing.T) {
	t.Parallel()

	for _, wildcard := range []rbac.ObjectAction{
		{Namespace: "*", Object: "*", Action: "*"},
		{Namespace: "**", Object: "**", Action: "**"},
		{Namespace: "*::*::*", Object: "*", Action: "*"},
		// A wildcard in one component only, against otherwise real names.
		{Namespace: "test", Object: "foo", Action: "*"},
	} {
		h := newTestHarness(t)

		_, err := h.service.Create(t.Context(), api_key.CreateRequest{
			UserID:      1,
			Name:        "wildcard",
			Permissions: []rbac.ObjectAction{wildcard},
		})

		require.Error(t, err, "%v must be refused", wildcard)
		assert.ErrorIs(t, err, api_key.ErrPermissionInvalid)
	}
}

// Nothing is written when any one of the requested actions is refused: a
// partially-honoured selection would be a key that silently grants less than
// the caller asked for, which is the failure this validation exists to prevent.
func TestCreateRefusesTheWholeRequestWhenOneActionIsInvalid(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	_, err := h.service.Create(t.Context(), api_key.CreateRequest{
		UserID: 1,
		Name:   "mixed",
		Permissions: []rbac.ObjectAction{
			registeredObjectAction,
			{Namespace: "test", Object: "foo", Action: "nope"},
		},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, api_key.ErrPermissionInvalid)
	h.repository.AssertNotCalled(t, "Create")
}

// A key naming no actions at all is legitimate: it falls back to the anonymous
// role, which is how an unscoped Torznab key works.
func TestCreateAllowsAnEmptySelection(t *testing.T) {
	t.Parallel()

	h := newTestHarness(t)

	h.repository.EXPECT().
		Create(t.Context(), 1, "unscoped", mock.IsType([]byte{}), []rbac.ObjectAction(nil), mock.IsType(time.Now())).
		Return(3, nil).
		Once()

	result, err := h.service.Create(t.Context(), api_key.CreateRequest{
		UserID: 1,
		Name:   "unscoped",
	})

	require.NoError(t, err)
	assert.Equal(t, 3, result.ID)
}
