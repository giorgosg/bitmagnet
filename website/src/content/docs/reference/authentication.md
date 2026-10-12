---
title: Authentication and Permissions
description: Understand Users, Roles, Invitations, and machine credentials.
---

## Identity and Roles

Every request resolves an Identity: anonymous, API key, or User authentication.
A Role is a persisted set of Permissions. A Permission assigns an Object action
(namespace, object, action) to a Role. The core Roles are `admin`, `editor`, `user`,
and `anon`; do not assume every signed-in User is an administrator.

An Invitation permits registration of a User. Registration requires one by default.
Administrators manage Invitations and assign Roles through the UI.

## Anonymous access

`auth.anonymous_access` controls whether the anonymous Role's stored Permissions apply.
A fresh instance does not grant catalogue access to that Role. Setting the flag to
`false` denies non-baseline anonymous operations even if Permissions are stored.
Version and health remain available for the browser shell.

If you enable public catalogue reads, grant only the read operations you intend.
Auth administration and destructive operations should remain restricted to authorised
Users. CORS controls browser cross-origin access; it does not replace authentication.

## Browser sessions

Magnes signs in using an HTTP-only Secure cookie. Use one HTTPS origin for the UI and
GraphQL, preserve its Host through the proxy, and configure a stable JWT secret.
Cookie-authenticated GraphQL mutations require an Origin matching that HTTPS Host.

Password changes revoke existing User sessions. API keys have a separate lifecycle;
review and revoke them separately when responding to a compromised credential.

## API keys

Create API keys through the signed-in User's API keys page. A key belongs to a User
and can be restricted to Object actions within that User's Permissions. Use separate
keys for separate clients and revoke those no longer needed. Key management itself
requires a User session, not another API key.

Torznab uses API keys. GraphQL and other supported HTTP surfaces can use machine
credentials according to their authentication contract. A Role change, disabled User,
expiry or revocation can affect a client's access.

See the repository's [operator authentication notes](https://github.com/giorgosg/magnes/blob/trunk/docs/auth.md)
for detailed endpoint contracts and recovery procedures.
