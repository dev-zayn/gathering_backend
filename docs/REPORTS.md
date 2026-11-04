# Reporting a player

The contract for a player reporting another, and an admin reading the reports.
The App Store asks any app with what players write and upload for a way to
report it and a timely answer; blocking (docs/FRIENDS.md) is the other half. The
platform (`backend/internal/biz/reports`, the player and admin routes, the
console) and the app (`gathering_app`, the player card) both build to it; when
they disagree this file decides, and when it is wrong it is fixed here first.

The reported player is never told, and nothing happens to them by itself: an
admin reads the report and decides, switching them off on the player's page if
it comes to that.

## `POST /v1/players/{player}/report` (operation `player-report`, scope `session`)

`{ "reason": "name|photo|harassment|cheating|other", "note"?: "..." }`, the
note up to 300 characters. **204**. One open report per reporter and player:
reporting again replaces its reason and note, and keeps its place in the list.

`name` covers what the player wrote about themselves: the nickname, the @handle
and the bio.

Refusals, in the order they are checked:

| Status | Code | When |
| --- | --- | --- |
| 422 | `invalid_fields` | `reason: required`, `reason: unknown`, `note: too_long`, `player: is_you` |
| 404 | `not_found` | no such player, or one switched off or deleted |
| 429 | `too_many_requests` | more than 20 reports an hour from one player (`retry_after`) |

Audit `report.filed` (area `players`): actor the reporter, subject the player,
`{reason}`; the note is not copied there.

## The console

### `GET /v1/admin/reports` (operation `admin-reports`, scope `admin`)

`?status=open|resolved|all` (default `open`; `status: unknown`), `limit` 1 to 100
(default 50; `limit: 1_to_100`), `cursor` (`cursor: invalid`). Newest first:

`{ items: [{ id, reason, note, status, created_at, resolved_at, resolved_by,
reporter: {id, nickname, handle}, player: {id, nickname, handle, avatar,
disabled}, against }], next_cursor, open }`

`against` is how many open reports name that player; `open` is how many are open
across the platform, whatever the filters. `resolved_by` is the admin's name.

### `POST /v1/admin/reports/{report}/resolve` (operation `admin-report-resolve`, role `admin`)

No body. **204**. `404 not_found`, `409 report_resolved`. It records that an admin
dealt with it; what they did is theirs to do on the player's page. Audit
`report.resolved` (area `players`): actor the admin, subject the report.

## Deleting an account

`DELETE /v1/me` (docs/ACCOUNTS.md) deletes the reports a player filed and the
reports against them: a note is the reporter's words, and nobody is left to act on.
