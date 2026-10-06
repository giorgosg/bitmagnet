package api_key

import (
	"crypto/sha256"
	"crypto/subtle"

	"golang.org/x/crypto/bcrypt"
)

// hashSecret returns what is stored against an API key secret: a plain
// SHA-256, with no salt, no pepper and no work factor.
//
// That is the right choice *for this credential* and would be wrong for a
// password. A password hash has to survive an offline attack against a secret
// drawn from a space an attacker can enumerate, which is what a work factor
// buys. An API key secret is secretLength bytes straight from crypto/rand - 96
// bits, which nobody searches, salted or peppered or neither. So bcrypt's cost
// bought nothing here, and it was charged on every request presenting a key,
// before any authorization check, against an unauthenticated caller's input:
// 55.8 ms of CPU per comparison, 18 per second per core, with a guessable key
// ID (api_keys.id is a serial, so 1 is valid). That is a CPU-exhaustion path
// open to anyone who can reach the port. SHA-256 answers the same comparison
// in about 100 ns.
//
// An HMAC under a server-side key was considered and rejected: a pepper also
// only helps when the input space is enumerable, so it would have bought
// nothing for this secret while costing a persisted secret with its own
// generation, rotation and backup story - and losing it would invalidate every
// key at once.
//
// What this trades is written down in docs/architecture/auth.md, and pinned by
// TestSecretLengthIsPinnedBecauseTheHashIsFast: bcrypt would have protected a
// *weak* secret, so from here safety rests on secretLength and crypto/rand
// staying as they are.
func hashSecret(secret []byte) []byte {
	sum := sha256.Sum256(secret)

	return sum[:]
}

// verifySecret compares a presented secret against a stored hash, and reports
// whether that hash was a bcrypt one from before hashSecret existed.
//
// Both formats have to be accepted, because there is no migration available: a
// bcrypt hash cannot be turned into a SHA-256 of the same secret without the
// plaintext, and the plaintext exists only in whatever the key's holder saved.
// So a legacy row is re-hashed when it next verifies, and nothing breaks in the
// meantime. The caller does that; see Auth.
//
// The two are told apart by width rather than by bcrypt's "$2" prefix, which is
// the obvious check and is subtly wrong: a SHA-256 digest is 32 uniformly
// random bytes, so one in 65,536 of them begins with the bytes "$2" and would
// be sent down the bcrypt path - permanently breaking that one key, at a rate
// high enough to actually happen. A bcrypt hash in the modular-crypt format this
// library emits is always 60 bytes and a digest is always sha256.Size, so the
// width decides it exactly.
func verifySecret(hash, secret []byte) (verified, legacy bool) {
	if len(hash) == sha256.Size {
		return subtle.ConstantTimeCompare(hash, hashSecret(secret)) == 1, false
	}

	// Anything that is not a digest is treated as bcrypt, including a hash of
	// some third shape: bcrypt rejects what it cannot parse, so an unreadable
	// row fails closed rather than authenticating.
	return bcrypt.CompareHashAndPassword(hash, secret) == nil, true
}
