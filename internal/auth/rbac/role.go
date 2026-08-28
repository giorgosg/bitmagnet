package rbac

import (
	"errors"
	"fmt"
	"regexp"
)

type Role string

// maxRoleNameLength bounds a role name. Nothing depends on the exact value; it
// exists so that a name cannot be unbounded.
const maxRoleNameLength = 32

// regexRoleName is the username character class from internal/auth/user, without
// a username's minimum length - a role is not a credential, and "ops" is a
// reasonable name for one.
//
// What it excludes is the point. A role name is stored as a casbin policy
// subject, and the matcher is globMatch(r.sub, p.sub), which globs the *stored*
// value against the request. A role named "*" would match every subject, anon
// included, and hand it that role's permissions. None of the characters allowed
// here are glob metacharacters.
var regexRoleName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// ErrRoleNameInvalid is returned for a role name that would be stored as a glob
// pattern, or that otherwise falls outside the permitted character class.
var ErrRoleNameInvalid = errors.New("invalid role name")

// Validate reports whether the name is safe to store as a policy subject.
func (r Role) Validate() error {
	if len(r) > maxRoleNameLength || !regexRoleName.MatchString(string(r)) {
		return fmt.Errorf("%w: must match %s and be at most %d characters",
			ErrRoleNameInvalid, regexRoleName.String(), maxRoleNameLength)
	}

	return nil
}

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleUser   Role = "user"
	RoleAnon   Role = "anon"
)

func (r Role) String() string {
	return string(r)
}

func CoreRoles() []Role {
	return []Role{
		RoleAdmin,
		RoleEditor,
		RoleUser,
		RoleAnon,
	}
}

type RoleInfo struct {
	Role
	Core        bool
	Permissions []Permission
}
