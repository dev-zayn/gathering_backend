package enum

// RoomRole is what a player is inside one room. The host runs the lobby; a
// spectator watches and never holds a seat. A room ticket carries exactly one.
type RoomRole string

const (
	RoomHost      RoomRole = "host"
	RoomPlayer    RoomRole = "player"
	RoomSpectator RoomRole = "spectator"
)

var RoomRoles = []RoomRole{RoomHost, RoomPlayer, RoomSpectator}

func (r RoomRole) Valid() bool { return in(RoomRoles, r) }
