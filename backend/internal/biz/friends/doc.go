// Package friends is who is whose friend: a request and its answers (accept,
// decline, cancel), a removal, a block and its lifting, where one player
// stands with another (the card), the friends with their presence, and the
// friends' leaderboard of a game. A request and an accept raise their
// notifications in the inbox, in the same transaction. Rooms ask here who
// may come into a friends-only room and who may be invited (Among).
// docs/FRIENDS.md is the contract.
package friends
