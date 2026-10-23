// Package rooms is the rules of a room: making one, joining it by code,
// leaving, kicking, starting a round and finishing it with scores (the room
// goes back to its lobby and plays again), ending the room, handing over the
// host, the ticket a game's server verifies, and the sweeper that finishes
// what runs too long and ends what nobody is in. Postgres holds who is in which room; every change is written to
// logs.room_events and published to the room's sockets through a Publisher,
// which the relay implements. The wire format — the Room and Member views
// and every frame — is defined here, once, for the HTTP answers and the
// relay alike. docs/ROOMS.md is the contract; the friends-only door and the
// invites (asking Friends, raising into the Inbox) are docs/FRIENDS.md's; the
// console's list, detail and close are docs/CONSOLE.md's.
package rooms
