// Package relay carries a room's frames between its members' sockets,
// whichever replica each socket landed on. One Hub per process holds the
// local sockets, one Redis subscription per room that has any, and the Lua
// scripts that make Redis the single order: every history frame takes the
// next seq, joins the room's log and goes out on the room's channel in one
// atomic step, so every member sees the same order. The hub never reads
// inside a frame's data; membership is the rooms' rule, asked through
// Directory. A socket is anything that satisfies Socket, so the rules here
// run without a network in the tests; the WebSocket library is http/ws's.
// docs/ROOMS.md is the contract.
package relay
