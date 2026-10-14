# Magnes

Magnes is an alternative browser interface for a bitmagnet instance. Its identity and authorization language is shared with bitmagnet so the UI, API, and operator documentation describe the same concepts.

## Language

### Principals and credentials

**Identity**:
The principal resolved for one request. Every request has exactly one Identity, whose credential kind is anonymous, API key, or User authentication.
_Avoid_: Principal, session, caller, login

**Anonymous identity**:
The Identity used when a request presents no usable credential. It has its own Permissions and is not the absence of an Identity.
_Avoid_: Guest, unauthenticated user, public user

**User**:
A persisted identity record for a person who signs in. An API-key Identity may report its owning User, but it is not a User-authenticated Identity.
_Avoid_: Account, member, profile

**API key**:
A persisted credential owned by a User and used by a machine caller, such as a \*arr client. When an API key is used, the Identity remains an API-key Identity even though it reports the owning User.
_Avoid_: Token, secret, api_key, machine account

**Invitation**:
A single-use code that permits its bearer to register a User.
_Avoid_: Invite code, signup link, registration token

### Authorization

**Object action**:
The unit of authorization: a namespace, object, and action considered together. It names an operation but does not say who may perform it.
_Avoid_: Scope, capability, right, verb

**Permission**:
The assignment of an Object action to a subject. It is the binding between a Role and an operation, not the operation itself.
_Avoid_: Rule, policy, ACL entry, grant

**Role**:
A named, persisted set of Permissions held by a User. `admin`, `editor`, `user`, and `anon` are the core Roles.
_Avoid_: Group, tier, access level

**Anonymous access**:
The instance setting that determines whether callers without a usable credential may use non-baseline operations. It is a property of the bitmagnet instance, not of an Identity.
_Avoid_: Auth enabled, public mode, open mode

### Moderation

**Moderation record**:
The exact pending, allowed or banned decision for a versioned torrent digest. Its private
evidence and history are separate from ordinary catalogue data.

**Moderator**:
An Identity with the explicit moderation Object actions. Ordinary catalogue reading and
tag editing do not imply this Permission; `moderator` can be a custom Role name.

**Revision**:
The opaque string returned with a moderation record or summary and passed unchanged as
`expectedRevision` for a decision. `updatedAt` is a display timestamp and must not be
converted back into a revision.
