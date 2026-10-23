package model

import "time"

// The audit log, and the one place its shape is written down.

// Entry is one line of the log. It is written as the decision is made, so there
// is nothing to reconstruct afterwards and nothing to disagree with.
type Entry struct {
	// ID is assigned by the log store. With the time it orders the log: it is
	// read newest first, and two decisions can share a millisecond.
	ID      string
	At      time.Time
	Area    string
	Actor   string
	Action  string
	Subject string
	Detail  map[string]any
}

// Query narrows the log. Every field is optional and they combine; an empty
// query is the whole log, newest first.
type Query struct {
	Actor   string
	Action  string
	Subject string

	// Actions is any of these: a reader after a kind of line, not one action.
	Actions []string

	// Search is one box over the whole line, because somebody looking has a
	// fragment and not the column it belongs to.
	Search string

	// Area narrows a reader that spans the whole log. A log opened for one area
	// ignores it: it can only ever read its own.
	Area string

	// The page, as rows to skip and rows to return.
	Offset int
	Limit  int
}

// The actions worth naming in one place, so a query for "who disabled this
// player" does not depend on remembering the spelling. Each area adds its own.
const (
	// Signing in. A refusal has no account to name, only the number that asked, so
	// that is what the subject holds — masking it would leave nothing actionable.
	ActionSignedIn = "session.opened"
	ActionRefused  = "session.refused"
	ActionSignedUp = "player.signed_up"

	// A player editing their own card: the name, handle, bio or language.
	ActionProfileEdited = "player.profile_edited"

	// An admin switching a player off, and back on.
	ActionPlayerDisabled = "player.disabled"
	ActionPlayerEnabled  = "player.enabled"

	// Who may open the console is the identity provider's to say, and it keeps
	// that record. Ours notes the first time somebody did, so a new name in the
	// log has a beginning.
	ActionAdminFirstSeen = "admin.first_seen"
	// An admin signed somebody out of the console everywhere: their role was
	// taken away at the provider, and the session they hold would outlive it.
	ActionAdminSessionsEnded = "admin.sessions_ended"

	// The registry. Registering reads a document; everything after it is a
	// decision about that document, made by an admin or by the clock.
	ActionRegistered   = "game.registered"
	ActionRefreshed    = "game.refreshed"
	ActionAccepted     = "game.accepted"
	ActionMoved        = "game.moved"
	ActionSecretRolled = "game.secret_rolled"
	ActionPublished    = "game.published"
	ActionSuspended    = "game.suspended"
	ActionFeatured     = "game.featured"
	// ActionListed is the store copy being written: which fields, never the
	// words, since the row holds them.
	ActionListed = "game.listed"

	// A player's library: the first open puts a game on it, a remove takes
	// it off and ends the game's tokens with it.
	ActionLibraryAdded   = "library.added"
	ActionLibraryRemoved = "library.removed"

	// An admin closing a room from the console, for everybody in it.
	ActionRoomClosed = "room.closed"

	// An admin's announcement: one system notification to many players.
	ActionNotificationSent = "notification.sent"

	// The console's hand in tournaments (docs/TOURNAMENTS.md, "The
	// console"): an official one made, any one started, cancelled or
	// finished, a match decided by an admin.
	ActionTournamentCreated   = "tournament.created"
	ActionTournamentStarted   = "tournament.started"
	ActionTournamentCancelled = "tournament.cancelled"
	ActionTournamentFinished  = "tournament.finished"
	ActionMatchAwarded        = "match.awarded"
)

// ActorWorker is written where a decision had nobody behind it. "The clock did"
// is a real answer, and better than an admin id nobody can look up.
const ActorWorker = "worker"
