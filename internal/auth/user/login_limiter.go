package user

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/bitmagnet-io/bitmagnet/internal/atomic"
	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/time/rate"
)

// loginLimiter throttles login attempts.
//
// It replaces a single process-wide rate.Limiter that every login waited on.
// That design had two failure modes, and an anonymous caller could trigger both:
// the budget was shared by every account, so five wrong guesses against names
// that do not exist locked out the entire deployment; and Login called Wait, so
// requests queued on the limiter and held a connection instead of being told to
// go away.
//
// Attempts are therefore counted against two independent buckets:
//
//   - the account as seen from one source, which is the bucket that actually
//     stops password guessing;
//   - the source alone, with a larger budget, which stops one host spraying
//     many accounts.
//
// Deliberately absent is a per-account bucket spanning all sources: it is the
// one key an attacker can fill on someone else's behalf, and doing so would
// lock the legitimate owner out from their own address. Bounding by
// (account, source) means an attacker's attempts only ever exhaust their own
// budget. The cost is that an attacker holding many addresses gets a few
// guesses from each; against a password that clears the entropy floor that is
// not a threat, and it is the right side of the trade against a lockout that
// anyone can trigger.
//
// The bucket map is an LRU with a fixed capacity, so it cannot be grown without
// bound by cycling keys. Eviction only ever resets a bucket, which fails toward
// availability rather than lockout.
type loginLimiter struct {
	mu      sync.Mutex
	buckets *lru.Cache[string, *rate.Limiter]
	limit   rate.Limit
	burst   int
}

const (
	// loginLimiterBuckets caps the tracked keys. At a few hundred bytes each
	// this is negligible memory, and far more distinct sources than a
	// self-hosted instance sees in a limiter window.
	loginLimiterBuckets = 4096

	// sourceBudgetFactor widens the per-source bucket relative to the
	// per-(account, source) one, so that a shared egress address — a household
	// behind NAT, a VPN exit — does not throttle its users against each other.
	sourceBudgetFactor = 4
)

func (rpm LoginRequestsPerMinute) limit() rate.Limit {
	return rate.Every(time.Minute / time.Duration(rpm))
}

func newLoginLimiter(
	rpm *atomic.Value[LoginRequestsPerMinute],
	burst *atomic.Value[LoginRequestBurst],
) *loginLimiter {
	buckets, err := lru.New[string, *rate.Limiter](loginLimiterBuckets)
	if err != nil {
		// Only returned for a non-positive size, which is a constant here.
		panic(err)
	}

	l := &loginLimiter{
		buckets: buckets,
		limit:   rpm.Get().limit(),
		burst:   int(burst.Get()),
	}

	// Existing buckets carry the old rate, so reconfiguring drops them. Losing
	// the accumulated counts is acceptable: config changes are rare and
	// operator-driven.
	rpm.Subscribe(func(rpm LoginRequestsPerMinute) {
		l.reconfigure(rpm.limit(), l.currentBurst())
	})

	burst.Subscribe(func(burst LoginRequestBurst) {
		l.reconfigure(l.currentLimit(), int(burst))
	})

	return l
}

// allow reports whether an attempt against username from source may proceed,
// and consumes a token from each bucket when it may. It never blocks.
func (l *loginLimiter) allow(username, source string) bool {
	// Usernames are matched case-sensitively by the lookup, but keying the
	// bucket that way would let case variations multiply the budget.
	account := strings.ToLower(username)

	if source == "" {
		// No source to key on — a non-HTTP caller, or a request that reached
		// the service without passing through the middleware. Bound the
		// account on its own rather than sharing one bucket between every
		// caller, which is the global limiter this replaced.
		return l.allowKeys(bucketKey{"account", account, 1})
	}

	return l.allowKeys(
		bucketKey{"account+source", account + "\x00" + source, 1},
		bucketKey{"source", source, sourceBudgetFactor},
	)
}

type bucketKey struct {
	scope string
	value string
	// factor scales both the rate and the burst for this bucket.
	factor int
}

func (k bucketKey) String() string {
	return k.scope + "\x00" + k.value
}

func (l *loginLimiter) allowKeys(keys ...bucketKey) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	taken := make([]*rate.Reservation, 0, len(keys))

	for _, key := range keys {
		reservation := l.bucketLocked(key).ReserveN(now, 1)

		// A reservation that cannot be honoured immediately is a refusal. Taking
		// it and waiting is what let a handful of attempts stall every other
		// login; the caller gets an error instead and the connection is freed.
		if !reservation.OK() || reservation.DelayFrom(now) > 0 {
			reservation.CancelAt(now)

			// Give back whatever the earlier keys granted, so a refusal by one
			// bucket does not silently drain the others.
			for _, granted := range taken {
				granted.CancelAt(now)
			}

			return false
		}

		taken = append(taken, reservation)
	}

	return true
}

func (l *loginLimiter) bucketLocked(key bucketKey) *rate.Limiter {
	cacheKey := key.String()

	if limiter, ok := l.buckets.Get(cacheKey); ok {
		return limiter
	}

	limiter := rate.NewLimiter(
		l.limit*rate.Limit(key.factor),
		l.burst*key.factor,
	)
	l.buckets.Add(cacheKey, limiter)

	return limiter
}

func (l *loginLimiter) reconfigure(limit rate.Limit, burst int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.limit = limit
	l.burst = burst
	l.buckets.Purge()
}

func (l *loginLimiter) currentLimit() rate.Limit {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.limit
}

func (l *loginLimiter) currentBurst() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.burst
}

// loginSourceKey carries the network origin of a request. It is set by the HTTP
// auth middleware, which is the only layer that knows it; a caller reaching the
// service by any other route simply has no source.
type loginSourceKey struct{}

// ContextWithLoginSource records the network origin of a request so that login
// throttling can be keyed by it as well as by account.
func ContextWithLoginSource(ctx context.Context, source string) context.Context {
	if source == "" {
		return ctx
	}

	return context.WithValue(ctx, loginSourceKey{}, source)
}

func loginSourceFromContext(ctx context.Context) string {
	source, _ := ctx.Value(loginSourceKey{}).(string)

	return source
}
