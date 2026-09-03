package rbac

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/slice"
	"github.com/casbin/casbin/v2"
)

type Enforcer interface {
	Enforce(ctx context.Context, subject Subject, objectAction ObjectAction) (bool, error)
	EnforceAny(ctx context.Context, subjects []Subject, objectAction ObjectAction) (bool, error)
	// EnforceEvery reports whether every group allows the object action, where a
	// group is satisfied when any one of its subjects allows it.
	//
	// It exists so that a decision needing more than one question costs one
	// acquisition of the service's semaphore rather than one per question. The
	// semaphore is process-global and the @auth directive fires per field, so the
	// difference is per field of every query.
	EnforceEvery(ctx context.Context, groups [][]Subject, objectAction ObjectAction) (bool, error)
	// FilterAllowed returns the object actions any of the subjects allows,
	// preserving the order they were given in.
	//
	// It exists so that a caller reporting what an identity may do asks casbin
	// rather than reimplementing the matcher. The policy side of that matcher
	// holds glob patterns - the admin role's permission is literally
	// "**::**::**" - so an intersection computed by equality would report that
	// an admin holds nothing, and any hand-rolled approximation is a second
	// source of truth that can drift from the decision it describes.
	//
	// One batch, so the whole set costs one acquisition of the semaphore.
	FilterAllowed(
		ctx context.Context,
		subjects []Subject,
		objectActions []ObjectAction,
	) ([]ObjectAction, error)
}

type Service interface {
	Enforcer
	Repository
	GetObjectActions() []ObjectAction
}

func NewService(
	repository Repository,
	objectActionProvider ObjectActionProvider,
	permissionProvider PermissionProvider,
	ttl CacheTTL,
) Service {
	return &service{
		sem:                  make(chan struct{}, 1),
		repository:           repository,
		objectActionProvider: objectActionProvider,
		permissionProvider:   permissionProvider,
		ttl:                  time.Duration(ttl),
	}
}

// service implements the Service interface backed by casbin.
// The design is partly working around the fact that casbin does not support context.
type service struct {
	sem                  chan struct{}
	ttl                  time.Duration
	lastUpdate           time.Time
	repository           Repository
	permissionProvider   PermissionProvider
	objectActionProvider ObjectActionProvider
	// roleMutex guards roleCache and roleCachedAt. It is separate from sem so a
	// role lookup does not queue behind a casbin decision, and vice versa.
	roleMutex    sync.RWMutex
	roleCache    []RoleInfo
	roleCachedAt time.Time
	*casbinDeps
}

type casbinDeps struct {
	*casbin.Enforcer
	*casbinAdapter
}

func (s *service) Enforce(ctx context.Context, subject Subject, objectAction ObjectAction) (bool, error) {
	return withCasbin(ctx, s, func(deps *casbinDeps) (bool, error) {
		return deps.Enforce(subjectString(subject), objectString(objectAction), objectAction.Action)
	})
}

func (s *service) EnforceAny(ctx context.Context, subjects []Subject, objectAction ObjectAction) (bool, error) {
	return withCasbin(ctx, s, func(deps *casbinDeps) (bool, error) {
		result, err := deps.BatchEnforce(batchRequests(subjects, objectAction))

		return slices.Contains(result, true), err
	})
}

func (s *service) EnforceEvery(
	ctx context.Context,
	groups [][]Subject,
	objectAction ObjectAction,
) (bool, error) {
	if len(groups) == 0 {
		return false, nil
	}

	return withCasbin(ctx, s, func(deps *casbinDeps) (bool, error) {
		// One batch for every group, then split the answers back out by group. casbin
		// evaluation is pure, so asking all of them is the same decision as asking
		// each in turn - it just costs one acquisition instead of one per group.
		var requests [][]any

		for _, subjects := range groups {
			if len(subjects) == 0 {
				// A group nothing can satisfy denies the whole decision.
				return false, nil
			}

			requests = append(requests, batchRequests(subjects, objectAction)...)
		}

		results, err := deps.BatchEnforce(requests)
		if err != nil {
			return false, err
		}

		blocks, err := splitBatchResults(results, slice.Map(groups, func(subjects []Subject) int {
			return len(subjects)
		}))
		if err != nil {
			return false, err
		}

		for _, block := range blocks {
			if !slices.Contains(block, true) {
				return false, nil
			}
		}

		return true, nil
	})
}

func (s *service) FilterAllowed(
	ctx context.Context,
	subjects []Subject,
	objectActions []ObjectAction,
) ([]ObjectAction, error) {
	if len(subjects) == 0 || len(objectActions) == 0 {
		return nil, nil
	}

	return withCasbin(ctx, s, func(deps *casbinDeps) ([]ObjectAction, error) {
		// One block of answers per object action, each block asking every subject.
		requests := make([][]any, 0, len(objectActions)*len(subjects))
		widths := make([]int, 0, len(objectActions))

		for _, objectAction := range objectActions {
			requests = append(requests, batchRequests(subjects, objectAction)...)
			widths = append(widths, len(subjects))
		}

		results, err := deps.BatchEnforce(requests)
		if err != nil {
			return nil, err
		}

		blocks, err := splitBatchResults(results, widths)
		if err != nil {
			return nil, err
		}

		allowed := make([]ObjectAction, 0, len(objectActions))

		for i, block := range blocks {
			if slices.Contains(block, true) {
				allowed = append(allowed, objectActions[i])
			}
		}

		return allowed, nil
	})
}

func (s *service) GetAllRoles(ctx context.Context) ([]RoleInfo, error) {
	return s.getRoles(ctx, nil)
}

func (s *service) GetRole(ctx context.Context, role Role) (RoleInfo, error) {
	roles, err := s.getRoles(ctx, []Role{role})
	if err != nil {
		return RoleInfo{}, err
	}

	return roles[0], nil
}

func (s *service) GetRoles(ctx context.Context, roles []Role) ([]RoleInfo, error) {
	if len(roles) == 0 {
		return nil, nil
	}

	return s.getRoles(ctx, roles)
}

func (s *service) getRoles(ctx context.Context, roles []Role) ([]RoleInfo, error) {
	all, err := s.cachedRoles(ctx)
	if err != nil {
		return nil, err
	}

	if roles == nil {
		return slices.Clone(all), nil
	}

	wanted := make(map[Role]struct{}, len(roles))
	for _, role := range roles {
		wanted[role] = struct{}{}
	}

	roleInfos := make([]RoleInfo, 0, len(roles))
	seenRoles := make(map[Role]struct{}, len(roles))

	for _, info := range all {
		if _, ok := wanted[info.Role]; !ok {
			continue
		}

		roleInfos = append(roleInfos, info)
		seenRoles[info.Role] = struct{}{}
	}

	missingRoles := slice.FlatMap(roles, func(role Role) []string {
		if _, ok := seenRoles[role]; !ok {
			return []string{string(role)}
		}

		return nil
	})

	if len(missingRoles) > 0 {
		return nil, fmt.Errorf("roles not found: %s", strings.Join(missingRoles, ", "))
	}

	return roleInfos, nil
}

// cachedRoles returns every role, core roles synthesised and permissions merged,
// refreshing from the repository at most once per RBACCacheTTL.
//
// Every authentication resolves a role - anonymous, JWT and API key alike - and the
// repository preloads permissions, so each lookup was two statements straight to
// the database. \*arr clients poll continuously, which made this the steady-state
// cost of an idle instance. The compiled casbin policy was already cached on this
// TTL; the roles behind it were not.
//
// The refresh itself runs under the write lock. It is one query once per TTL, and
// serialising the refresh is what keeps a stampede of authentications from each
// issuing their own.
func (s *service) cachedRoles(ctx context.Context) ([]RoleInfo, error) {
	s.roleMutex.RLock()

	if s.roleCache != nil && time.Since(s.roleCachedAt) <= s.ttl {
		defer s.roleMutex.RUnlock()

		return s.roleCache, nil
	}

	s.roleMutex.RUnlock()

	s.roleMutex.Lock()
	defer s.roleMutex.Unlock()

	// Another goroutine may have refreshed while this one waited for the lock.
	if s.roleCache != nil && time.Since(s.roleCachedAt) <= s.ttl {
		return s.roleCache, nil
	}

	roleInfos, err := s.repository.GetAllRoles(ctx)
	if err != nil {
		return nil, err
	}

	seenRoles := make(map[Role]struct{}, len(roleInfos))
	for _, role := range roleInfos {
		seenRoles[role.Role] = struct{}{}
	}

	for _, coreRole := range CoreRoles() {
		if _, ok := seenRoles[coreRole]; !ok {
			roleInfos = append(roleInfos, RoleInfo{
				Role: coreRole,
				Core: true,
			})

			seenRoles[coreRole] = struct{}{}
		}
	}

	roleInfos = slice.Map(roleInfos, s.mergeCoreRolePermissions)

	slices.SortFunc(roleInfos, func(a, b RoleInfo) int {
		return cmp.Compare(a.Role, b.Role)
	})

	s.roleCache = roleInfos
	s.roleCachedAt = time.Now()

	return roleInfos, nil
}

// invalidateRoleCache drops the snapshot after this process writes a role, so an
// administrator sees their own change rather than waiting out the TTL.
func (s *service) invalidateRoleCache() {
	s.roleMutex.Lock()
	defer s.roleMutex.Unlock()

	s.roleCache = nil
}

func (s *service) GetPermissions(ctx context.Context) ([]Permission, error) {
	return withCasbin(ctx, s, func(deps *casbinDeps) ([]Permission, error) {
		return deps.permissions, nil
	})
}

func (s *service) PutRole(ctx context.Context, role Role, objectActions []ObjectAction) (RoleInfo, error) {
	// Before the semaphore and before the repository: the name becomes a casbin
	// policy subject, and the matcher globs the stored value against the request.
	if err := role.Validate(); err != nil {
		return RoleInfo{}, err
	}

	return withSem(ctx, s, func() (RoleInfo, error) {
		roleInfo, err := s.repository.PutRole(ctx, role, objectActions)
		if err != nil {
			return RoleInfo{}, err
		}

		s.invalidateRoleCache()

		if !s.lastUpdate.IsZero() {
			if err = s.updatePermissions(ctx); err != nil {
				return RoleInfo{}, err
			}
		}

		return s.mergeCoreRolePermissions(roleInfo), nil
	})
}

func (s *service) DeleteRole(ctx context.Context, role Role) error {
	if slices.Contains(CoreRoles(), role) {
		return errors.New("core roles cannot be deleted")
	}

	return withSemErr(ctx, s, func() error {
		if err := s.repository.DeleteRole(ctx, role); err != nil {
			return err
		}

		s.invalidateRoleCache()

		if !s.lastUpdate.IsZero() {
			return s.updatePermissions(ctx)
		}

		return nil
	})
}

func (s *service) GetObjectActions() []ObjectAction {
	return s.objectActionProvider()
}

// withSem runs fn while holding the service's one-slot semaphore.
//
// casbin has no context support, so every interaction with it is serialised
// behind this one channel. That makes *how often* the semaphore is taken a
// property of the whole server rather than of any one method, and seven
// hand-written copies of the select-and-defer pair made it impossible to count
// at a glance -- which is the question the outstanding scalability work has to
// answer. Keeping the protocol in one place also removes the way to get it
// wrong: a caller cannot forget the release, because it is not theirs to write.
//
// A free function rather than a method because Go does not allow methods to
// take type parameters.
func withSem[T any](ctx context.Context, s *service, fn func() (T, error)) (T, error) {
	var zero T

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case s.sem <- struct{}{}:
	}

	defer func() { <-s.sem }()

	return fn()
}

// withSemErr is withSem for a call that reports only an error.
func withSemErr(ctx context.Context, s *service, fn func() error) error {
	_, err := withSem(ctx, s, func() (struct{}, error) {
		return struct{}{}, fn()
	})

	return err
}

// withCasbin is withSem for the calls that go on to talk to casbin. acquireCasbin
// initialises s.casbinDeps on first use, so it has to run inside the semaphore
// too, not before it.
func withCasbin[T any](ctx context.Context, s *service, fn func(*casbinDeps) (T, error)) (T, error) {
	return withSem(ctx, s, func() (T, error) {
		deps, err := s.acquireCasbin(ctx)
		if err != nil {
			var zero T

			return zero, err
		}

		return fn(deps)
	})
}

// acquireCasbin returns the casbin enforcer instance, initializing it if necessary.
func (s *service) acquireCasbin(ctx context.Context) (*casbinDeps, error) {
	if s.casbinDeps == nil {
		permissions, err := s.repository.GetPermissions(ctx)
		if err != nil {
			return nil, err
		}

		adapterDynamic := &casbinAdapter{
			permissions: append(s.permissionProvider(), permissions...),
		}

		eCasbin, err := newCasbinEnforcer(adapterDynamic)
		if err != nil {
			return nil, err
		}

		s.casbinDeps = &casbinDeps{
			casbinAdapter: adapterDynamic,
			Enforcer:      eCasbin,
		}

		s.lastUpdate = time.Now()
	} else if time.Since(s.lastUpdate) > s.ttl {
		if err := s.updatePermissions(ctx); err != nil {
			return nil, err
		}
	}

	return s.casbinDeps, nil
}

func (s *service) updatePermissions(ctx context.Context) error {
	permissions, err := s.getPermissions(ctx)
	if err != nil {
		return err
	}

	s.setPermissions(permissions)

	err = s.Enforcer.LoadPolicy()
	if err != nil {
		return err
	}

	s.lastUpdate = time.Now()

	return nil
}

func (s *service) getPermissions(ctx context.Context) ([]Permission, error) {
	perms := make(map[string]map[ObjectAction]Permission)

	repoPerms, err := s.repository.GetPermissions(ctx)
	if err != nil {
		return nil, err
	}

	applyPerm := func(perm Permission) {
		subj := subjectString(perm)
		if _, ok := perms[subj]; !ok {
			perms[subj] = make(map[ObjectAction]Permission)
		}

		perms[subj][perm.ObjectAction()] = perm
	}

	for _, perm := range repoPerms {
		applyPerm(perm)
	}

	for _, perm := range s.permissionProvider() {
		applyPerm(perm)
	}

	var finalPerms []Permission

	for _, objAct := range perms {
		for _, perm := range objAct {
			finalPerms = append(finalPerms, perm)
		}
	}

	sortPermissions(finalPerms)

	return finalPerms, nil
}

func subjectString(sub Subject) string {
	return fmt.Sprintf("%s::%s", sub.SubjectType(), sub.SubjectName())
}

func objectString(objAct ObjectAction) string {
	return fmt.Sprintf("%s::%s", objAct.Namespace, objAct.Object)
}

// splitBatchResults cuts one BatchEnforce answer list into the blocks it was
// assembled from. Both batching callers build their request list as consecutive
// blocks and then have to read it back the same way, and getting the two out of
// step would silently attribute one question's answer to another.
//
// casbin returns one result per request, so a short list means the enforcer
// broke its own contract; that is an error rather than a slice-bounds panic on
// an authorization path.
func splitBatchResults(results []bool, widths []int) ([][]bool, error) {
	total := 0
	for _, width := range widths {
		total += width
	}

	if len(results) != total {
		return nil, fmt.Errorf("casbin returned %d results for %d requests", len(results), total)
	}

	blocks := make([][]bool, 0, len(widths))
	offset := 0

	for _, width := range widths {
		blocks = append(blocks, results[offset:offset+width])
		offset += width
	}

	return blocks, nil
}

func batchRequests(subs []Subject, objAct ObjectAction) [][]any {
	return slice.Map(subs, func(sub Subject) []any {
		return []any{
			subjectString(sub),
			objectString(objAct),
			objAct.Action,
		}
	})
}

func (s *service) mergeCoreRolePermissions(roleInfo RoleInfo) RoleInfo {
	permMap := make(map[ObjectAction]bool)
	for _, perm := range roleInfo.Permissions {
		permMap[perm.ObjectAction()] = false
	}

	for _, perm := range s.permissionProvider() {
		if perm.SubjectType() == SubjectTypeRole && perm.SubjectName() == string(roleInfo.Role) {
			permMap[perm.ObjectAction()] = true
		}
	}

	perms := make([]Permission, 0, len(permMap))
	for objAct, core := range permMap {
		perms = append(perms, permission{
			Subject: SubjectRole{
				Role: roleInfo.Role,
			},
			objectAction: objAct,
			core:         core,
		})
	}

	sortPermissions(perms)

	return RoleInfo{
		Role:        roleInfo.Role,
		Core:        slices.Contains(CoreRoles(), roleInfo.Role),
		Permissions: perms,
	}
}

func sortPermissions(perms []Permission) {
	slices.SortFunc(perms, func(a, b Permission) int {
		r := cmp.Compare(subjectString(a), subjectString(b))
		if r != 0 {
			return r
		}

		return a.ObjectAction().Compare(b.ObjectAction())
	})
}
