// Package guests is what a game's own server holds and asks: the launch
// that turns a player's session into a one-time code, the exchange that turns
// the code into a credential of the game's own, and the bridge that answers
// who arrived. A guest's credential is minted for one game and is worth
// nothing anywhere else.
package guests
