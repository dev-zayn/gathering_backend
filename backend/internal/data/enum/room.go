package enum

// RoomStatus is where a room is in its life: the lobby fills, a round is
// played, the lobby again after every round, and the room is over. Ended is
// for good: an ended room never reopens, and its code is free for the next
// one.
type RoomStatus string

const (
	RoomLobby   RoomStatus = "lobby"
	RoomPlaying RoomStatus = "playing"
	RoomEnded   RoomStatus = "ended"
)

var RoomStatuses = []RoomStatus{RoomLobby, RoomPlaying, RoomEnded}

func (s RoomStatus) Valid() bool { return in(RoomStatuses, s) }

// Open is a room a player can still be in: the lobby, or the game.
func (s RoomStatus) Open() bool { return s == RoomLobby || s == RoomPlaying }

// Visibility is who may join a room. Private is anybody with the code;
// friends is the host's friends alone, with the code or an invite.
type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityFriends Visibility = "friends"
)

var Visibilities = []Visibility{VisibilityPrivate, VisibilityFriends}

func (v Visibility) Valid() bool { return in(Visibilities, v) }

// EndReason is why a room ended: the host said so, the last member left, the
// lobby sat idle, the game ran past its limit, an admin closed it from the
// console, or the tournament match it was made for was decided
// (docs/TOURNAMENTS.md). Only the host's names somebody: the clock, an empty
// room and the console end it with nobody behind it on the wire. A round
// finishes for two of them: the host's finish (host), and the clock's on a
// round past its limit (timeout).
type EndReason string

const (
	EndHost    EndReason = "host"
	EndEmpty   EndReason = "empty"
	EndIdle    EndReason = "idle"
	EndTimeout EndReason = "timeout"
	EndAdmin   EndReason = "admin"

	// EndTournament is a match room whose match was decided — by the round
	// that just closed, or by an award, a walkover, a finish or a
	// cancellation — or whose tournament is over.
	EndTournament EndReason = "tournament"
)

var EndReasons = []EndReason{EndHost, EndEmpty, EndIdle, EndTimeout, EndAdmin, EndTournament}

func (r EndReason) Valid() bool { return in(EndReasons, r) }

// RoomEvent is one line of a room's history in logs.room_events: every
// transition writes exactly one.
type RoomEvent string

const (
	EventCreated RoomEvent = "created"
	EventJoined  RoomEvent = "joined"
	EventLeft    RoomEvent = "left"
	EventKicked  RoomEvent = "kicked"
	EventStarted RoomEvent = "started"
	EventEnded   RoomEvent = "ended"
	EventHost    RoomEvent = "host"

	// EventResults is a round's results written: by the host's finish or end,
	// or by the game's own server.
	EventResults RoomEvent = "results"

	// EventFinished is a round over and the room back in its lobby: the
	// host's finish, or the clock's on a round past its limit.
	EventFinished RoomEvent = "finished"
)

var RoomEvents = []RoomEvent{
	EventCreated, EventJoined, EventLeft, EventKicked, EventStarted, EventEnded, EventHost, EventResults, EventFinished,
}

func (e RoomEvent) Valid() bool { return in(RoomEvents, e) }

// Submitter is who wrote a round's results: the host, finishing the round or
// ending the room with scores, or the game's own server through the bridge.
// The game's word is the stronger: once it has written a round's results,
// the host's finish or end leaves them alone.
type Submitter string

const (
	SubmitterHost Submitter = "host"
	SubmitterGame Submitter = "game"
)

var Submitters = []Submitter{SubmitterHost, SubmitterGame}

func (s Submitter) Valid() bool { return in(Submitters, s) }
