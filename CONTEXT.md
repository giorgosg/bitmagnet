# bitmagnet

A BitTorrent indexer, DHT crawler and torrent search engine. This glossary fixes the
vocabulary that is specific to bitmagnet — it is not a specification, and it deliberately
says nothing about how anything is built.

## Language

### Principals and credentials

**Identity**:
The principal resolved for a single request. Every request has exactly one, and it is
either anonymous, an API key, or a user bearing a token.
_Avoid_: Principal, session, caller, login

**Anonymous identity**:
The Identity carried by a request that presented no usable credential. It is a real
Identity with real permissions, not the absence of one.
_Avoid_: Guest, unauthenticated user, public user

**User**:
A persisted account a person logs in to. A request may have an Identity without a User;
machine callers always do.
_Avoid_: Account, member, profile

**API key**:
A persisted credential belonging to a machine caller rather than a person. It is the only
credential form \*arr clients and other automation use.
_Avoid_: Token, secret, api\_key, machine account

**Invitation**:
A single-use code granting its bearer the right to register a User.
_Avoid_: Invite code, signup link, registration token

### Authorization

**Object action**:
The unit of authorization — a namespace, an object and an action taken together, such as
`graphql`/`torznab`/`query`. It names something that may be done, and says nothing about
who may do it.
_Avoid_: Scope, capability, right, verb

**Permission**:
The grant of one Object action to one subject. It is the binding, not the thing bound.
_Avoid_: Rule, policy, ACL entry, grant

**Role**:
A named, persisted set of Permissions that a User holds. `admin`, `editor`, `user` and
`anon` are the core roles.
_Avoid_: Group, tier, access level

**Anonymous access**:
The operator-facing switch deciding whether this instance answers callers who present no
credential. It is a property of the instance, not of any Identity.
_Avoid_: Auth enabled, public mode, open mode

---

Only the authentication vocabulary is settled so far. The indexing terms — torrent,
torrent source, content, classification — are used loosely across the codebase and docs
and have not been reconciled; add them here when they are.
