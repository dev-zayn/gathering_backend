package enum

// TournamentFormat is how a tournament is played: duels in a knockout
// bracket, or everybody at one table for a set number of rounds.
type TournamentFormat string

const (
	FormatKnockout TournamentFormat = "knockout"
	FormatLeague   TournamentFormat = "league"
)

var TournamentFormats = []TournamentFormat{FormatKnockout, FormatLeague}

func (f TournamentFormat) Valid() bool { return in(TournamentFormats, f) }

// TournamentStatus is where a tournament is in its life: taking entrants,
// being played, over with a champion, or called off. Done and cancelled are
// for good, and free the code.
type TournamentStatus string

const (
	TournamentOpen      TournamentStatus = "open"
	TournamentRunning   TournamentStatus = "running"
	TournamentDone      TournamentStatus = "done"
	TournamentCancelled TournamentStatus = "cancelled"
)

var TournamentStatuses = []TournamentStatus{TournamentOpen, TournamentRunning, TournamentDone, TournamentCancelled}

func (s TournamentStatus) Valid() bool { return in(TournamentStatuses, s) }

// Live is a tournament still holding its code: taking entrants, or played.
func (s TournamentStatus) Live() bool { return s == TournamentOpen || s == TournamentRunning }

// TournamentVisibility is who may come in. Private is anybody with the code;
// friends is the organizer's friends alone; public is an official one, made
// from the console, which the app lists for everybody.
type TournamentVisibility string

const (
	TournamentPrivate TournamentVisibility = "private"
	TournamentFriends TournamentVisibility = "friends"
	TournamentPublic  TournamentVisibility = "public"
)

var TournamentVisibilities = []TournamentVisibility{TournamentPrivate, TournamentFriends, TournamentPublic}

func (v TournamentVisibility) Valid() bool { return in(TournamentVisibilities, v) }

// TournamentEnd is why a tournament is over. Done: its last match was decided
// (played), the organizer or an admin finished a league early (early), or a
// league was left with fewer than two entrants (withdrawals). Cancelled: the
// organizer, an admin, the clock at its start time short of entrants
// (too_few), or the clock after it sat open too long (expired).
type TournamentEnd string

const (
	TournamentPlayed      TournamentEnd = "played"
	TournamentEarly       TournamentEnd = "early"
	TournamentWithdrawals TournamentEnd = "withdrawals"
	TournamentByOrganizer TournamentEnd = "organizer"
	TournamentByAdmin     TournamentEnd = "admin"
	TournamentTooFew      TournamentEnd = "too_few"
	TournamentExpired     TournamentEnd = "expired"
)

var TournamentEnds = []TournamentEnd{
	TournamentPlayed, TournamentEarly, TournamentWithdrawals,
	TournamentByOrganizer, TournamentByAdmin, TournamentTooFew, TournamentExpired,
}

func (e TournamentEnd) Valid() bool { return in(TournamentEnds, e) }

// MatchStatus is where a match stands: a side still to be decided, ready to
// be played, or decided.
type MatchStatus string

const (
	MatchWaiting MatchStatus = "waiting"
	MatchReady   MatchStatus = "ready"
	MatchDone    MatchStatus = "done"
)

var MatchStatuses = []MatchStatus{MatchWaiting, MatchReady, MatchDone}

func (s MatchStatus) Valid() bool { return in(MatchStatuses, s) }

// MatchDecision is how a match was decided: played to its wins, a seed
// facing nobody (bye), an opponent who withdrew (walkover), the organizer or
// an admin (award), or a league's table when it ended (standings).
type MatchDecision string

const (
	DecidedByPlay      MatchDecision = "play"
	DecidedByBye       MatchDecision = "bye"
	DecidedByWalkover  MatchDecision = "walkover"
	DecidedByAward     MatchDecision = "award"
	DecidedByStandings MatchDecision = "standings"
)

var MatchDecisions = []MatchDecision{
	DecidedByPlay, DecidedByBye, DecidedByWalkover, DecidedByAward, DecidedByStandings,
}

func (d MatchDecision) Valid() bool { return in(MatchDecisions, d) }
