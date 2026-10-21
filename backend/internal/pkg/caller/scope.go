package caller

// The kinds of token. A kind is what a route declares it needs; a token of the
// wrong kind is refused outright, not treated as a weaker one.
const (
	// ScopeOTP is a phone number with a code on its way: it opens the verify
	// and resend calls and nothing else.
	ScopeOTP = "otp"
	// ScopeSignup is a proven number with no player behind it yet: it opens
	// the one call that creates one.
	ScopeSignup = "signup"
	// ScopeSession is a player, on one device.
	ScopeSession = "session"
	// ScopeAdmin is an admin signed in through the identity
	// provider: every player's record, and nothing a player token opens. We
	// mint these ourselves once the provider has said who the person is.
	ScopeAdmin = "admin"
	// ScopeGame is a game's backend acting for a player who opened it.
	ScopeGame = "game"
	// ScopeRoom is a room ticket: one player, in one room, for one game to
	// verify. It opens nothing on this API; it is for the game's own server.
	ScopeRoom = "room"
)
