package config

import (
	"github.com/bitmagnet-io/bitmagnet/internal/gql"
	gqlauth "github.com/bitmagnet-io/bitmagnet/internal/gql/auth"
	"github.com/bitmagnet-io/bitmagnet/internal/gql/resolvers"
	"github.com/bitmagnet-io/bitmagnet/internal/lazy"
	"go.uber.org/fx"
)

type Params struct {
	fx.In
	ResolverRoot lazy.Lazy[*resolvers.Resolver]
}

func New(p Params) lazy.Lazy[gql.Config] {
	return lazy.New(func() (gql.Config, error) {
		root, err := p.ResolverRoot.Get()
		if err != nil {
			return gql.Config{}, err
		}

		// The @auth directive is what enforces authorization on the GraphQL
		// surface. Without it the identity is resolved and then ignored.
		return gql.Config{
			Resolvers: root,
			Directives: gql.DirectiveRoot{
				Auth: gqlauth.NewDirective(),
			},
		}, nil
	})
}
