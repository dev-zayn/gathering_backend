# Deleting an account

The contract for a player deleting their own account from the app, which
both stores ask for: Google Play links to it, and the App Store wants it
started inside the app. The platform (`backend/internal/biz`, the player
routes) and the app (`gathering_app`, the Me tab) both build to it; when
they disagree this file decides, and when it is wrong it is fixed here first.

What goes is everything that is the person's: how they sign in, what others
find them by, and what reaches them. What stays is the games already played,
which belong to the other players' history too: the player's row is kept,
emptied, under the name «لاعب محذوف», so a result, a bracket or a room's
history still has somebody in that seat.

## `DELETE /v1/me` (operation `me-delete`, scope `session`)

No body. **204** once it is done; the token that asked is dead with every
other session of the player's. The app asks first ("are you sure") and then
signs out on the phone.

Refusals, in the order they are checked:

| Status | Code | When |
| --- | --- | --- |
| 409 | `in_room` | a current member of a room that has not ended: leave it first |
| 409 | `in_tournament` | the organizer of a tournament still open or running, or an entrant of one who has not left and was not removed: leave it or wait for it to end |

All of it in one transaction, under the player's row lock:

- **Deleted:** the player's identities (Google, Apple), devices, friendships
  and blocks (either side), notifications to them and from them, their
  library, their games' refresh tokens and one-time codes.
- **Ended:** every session (`revoked_at`), and after the commit every token
  of every session and of every game, through the whitelist; presence cleared.
- **Emptied, kept:** the `players` row: `phone` null, `nickname` «لاعب محذوف»,
  `handle` `deleted_` and the id's last twelve characters, `avatar_key` and
  `bio` empty, `disabled` true, `deleted_at` now (migration 00014). The
  uploaded picture, if any, is deleted from storage after the commit.
- **Kept as they are:** results, rooms and their members, tournaments, their
  entrants and matches, and the logs (`logs.audit`, `logs.room_events`).

After it, the phone number and the Google or Apple account are free: signing
in with either again makes a new player. The audit line is
`player.deleted` (area `players`), actor and subject the player, no detail.

Apple: the app never asks Apple for a refresh token (docs/SSO.md), so there
is none to revoke; deleting the identities row is the whole of it.

## By email

A player without the app writes to the address on the privacy page; an
admin does the same deletion by hand. The console has no button for it yet.
