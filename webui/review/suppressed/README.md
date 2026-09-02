# Suppressed findings

`NoUnused.CustomTypeConstructorArgs` reported the `User` and API-key-name payloads on
`Identity.UserAuthenticated` and `Identity.APIKeyAuthenticated`. They were genuinely never
read, and the rule was right about that — but they are what ticket 10 (User overview and
sign-out) and ticket 11 (API-key management) display. Removing them to satisfy the rule
would have meant re-adding them in the next ticket.

The `User` payload is now read: `Roles.ownRole` matches both constructors for the Role the
viewer holds, which is what makes saving that Role self-affecting. The count fell from two
to one on its own, exactly as intended.

**What is left is the API-key name on `APIKeyAuthenticated`**, which ticket 11 displays.
Drop the suppression entirely once that ticket lands: the count should reach zero by
itself, and `elm-review` fails if a suppressed count is exceeded, so it cannot quietly
grow. Note that `elm-review suppress` rewrites this directory and **deletes this file** —
restore it after running that command.
