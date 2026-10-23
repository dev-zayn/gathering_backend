// Package inbox is a player's notifications: raising one (a friend asking or
// accepting, an invite to a room, a game's own server), the feed newest first
// a page at a time with the unread count, marking read, and the housekeeping
// that forgets the old. A raise with a key already in the inbox updates that
// row in place and brings it back to the top, unread. docs/FRIENDS.md is the
// contract; the console's announcements (one system notification to many
// players, Announce and Announcements) are docs/CONSOLE.md's.
package inbox
