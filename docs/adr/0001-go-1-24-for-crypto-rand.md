# Go 1.24 for the `crypto/rand` guarantee

The authentication work generates three pieces of secret material from
`crypto/rand` — the per-process JWT signing secret, the bootstrap administrator
invitation, and every API key. Under Go 1.23 `rand.Read` returns an error and leaves the
buffer untouched, so a discarded error silently yields an all-zero secret: a JWT key of
zero and an administrator invitation of zero, with nothing in the log to say so. Go 1.24
made `crypto/rand.Read` incapable of returning an error — it terminates the process if
the system entropy source fails — which is the correct outcome for a credential. The
module, the `Dockerfile` and the CI pins therefore move to 1.24 together.

**Updated 2026-08-22:** trunk has since gone past this floor. Closing four called
vulnerabilities required `x/text`, `x/net` and `pgx`, every fixed version of which
declares `go 1.25.0`, so the module now targets 1.25 and the images build on 1.26. That
supersedes the number in this record without disturbing the decision — 1.25 is above
1.24, so the `crypto/rand` guarantee still holds. The floor must never drop back below
1.24.

## Consequences

The error is still checked at the call site rather than left implied by the `go`
directive, so the code reads correctly to someone who does not know this decision was
made.

**The bump is not confined to `go.mod`.** Raising the directive switched on the
`usetesting` lint rule, which requires `t.Context()` as soon as the module targets 1.24 —
50 call sites across 29 files, and the reason the first CI run on the port branch went
red. Expect any future directive bump to change the active linter set the same way.

Raising the floor is what makes this hard to reverse: it constrains every contributor's
toolchain and the release image, and dropping back would silently reintroduce the
zero-secret path.
