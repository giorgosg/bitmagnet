package api_key

import (
	"context"

	"github.com/bitmagnet-io/bitmagnet/internal/auth/rbac"
	"github.com/bitmagnet-io/bitmagnet/internal/model"
)

type Service interface {
	Create(ctx context.Context, req CreateRequest) (CreateResult, error)
	Auth(ctx context.Context, key string) (model.APIKey, error)
	List(ctx context.Context, req ListRequest) (ListResult, error)
	Delete(ctx context.Context, req DeleteRequest) error
}

// NewService takes the registered object actions rather than the rbac service:
// a key's permissions are checked against the registry, and depending on the
// service itself would put api_key inside the cycle rbac.ServiceLazy exists to
// break. The provider is the same one listObjectActions serves.
func NewService(repository Repository, objectActions rbac.ObjectActionProvider) Service {
	return service{
		repository:    repository,
		objectActions: objectActions,
	}
}

type service struct {
	repository    Repository
	objectActions rbac.ObjectActionProvider
}
