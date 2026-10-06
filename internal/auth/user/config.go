package user

// The settings these name are live, and reach this package as
// authconfig.Config fields through authfx; the names exist so the values carry
// their meaning rather than being bare bools and ints.
type (
	InvitationRequired bool

	EmailRequired bool

	EmailVerification bool

	PasswordMinEntropy float64

	PasswordHashingCost int

	LoginRequestsPerMinute int

	LoginRequestBurst int
)
