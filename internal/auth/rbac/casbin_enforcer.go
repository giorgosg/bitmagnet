package rbac

import (
	_ "embed"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
)

//go:embed casbin_model.conf
var embedModel string

// newCasbinEnforcer builds a *synced* enforcer. casbin's plain Enforcer is not
// safe for concurrent use, and this one is on the path of every authorization
// decision in the process; SyncedEnforcer takes a read lock to enforce and a
// write lock to load, which is what lets decisions run concurrently with each
// other and still never see a half-loaded policy.
func newCasbinEnforcer(adp persist.Adapter) (*casbin.SyncedEnforcer, error) {
	e, err := casbin.NewSyncedEnforcer()
	if err != nil {
		return nil, err
	}

	mdl, err := model.NewModelFromString(embedModel)
	if err != nil {
		return nil, err
	}

	err = e.InitWithModelAndAdapter(mdl, adp)
	if err != nil {
		return nil, err
	}

	return e, nil
}
