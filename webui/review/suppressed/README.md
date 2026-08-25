# Suppressed findings

`NoUnused.CustomTypeConstructorArgs` reports the `User` and API-key-name payloads on
`Identity.UserAuthenticated` and `Identity.APIKeyAuthenticated`. They are genuinely never
read *today*, and the rule is right about that — but they are what ticket 10 (User
overview and sign-out) and ticket 11 (API-key management) display. Removing them to
satisfy the rule would mean re-adding them in the next ticket.

Drop the suppression once those tickets land: the count should fall to zero on its own,
and `elm-review` fails if a suppressed count is exceeded, so this cannot quietly grow.
