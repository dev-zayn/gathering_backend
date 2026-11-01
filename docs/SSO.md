# Signing in with Google or Apple

The contract for a player signing in through a provider instead of a phone
and a code. The platform (`backend/internal/biz/identity`, `biz.Signup`,
`conf`) and the app (`gathering_app`) both build to it; when they disagree
this file decides, and when it is wrong it is fixed here first.

The app shows **Google** on Android and iOS, and **Apple** on iOS only (the
App Store asks for Apple wherever Google is offered). The phone and its code
stay as they are, next to them.

A provider account is a player of its own: it needs no phone, and it is not
matched to a player who signed up with one. Linking the two is for later.

## `POST /v1/auth/sso/{provider}` (operation `auth-sso`, public)

`{provider}` is `google` or `apple`; anything else is huma's `422`. The body
is `{ "token": "<the provider's id token>" }`, the JWT the provider's SDK
handed the phone:

- Google: the id token from Google Sign-In, whose `aud` is one of
  `GOOGLE_CLIENT_IDS` (the web client the app names as its server client,
  and the Android and iOS clients), issuer `https://accounts.google.com`.
- Apple: the identity token from Sign in with Apple, whose `aud` is one of
  `APPLE_CLIENT_IDS` (the app's bundle id), issuer `https://appleid.apple.com`.

The token is checked against the provider's published keys: signature,
issuer, audience, expiry, and a `sub`. Nothing else in it is read or kept:
not the name, not the address. The player is the pair (provider, `sub`), in
`identities`.

The answer is the one `auth-otp-verify` gives:

- a pair we know: **200** `{ scope: "session", access_token, refresh_token,
  expires_in, player }`, and an audit line `signed_in` with `{provider}`;
- a pair nobody has: **200** `{ scope: "signup", access_token, expires_in }`
  and nothing written. The signup token carries the pair instead of a phone,
  for `SIGNUP_TTL`.

Refusals, in the order they are checked:

| Status | Code | When |
| --- | --- | --- |
| 501 | `not_supported` | the provider has no client ids on this deployment |
| 502 | `provider_unavailable` | the provider's keys could not be fetched; nobody is let in meanwhile |
| 401 | `invalid_provider_token` | not a good token from that provider for this app: signature, issuer, audience, expiry, no `sub` |
| 403 | `player_disabled` | the pair's player is switched off (audit `refused`, `{reason: disabled}`) |

## `POST /v1/auth/signup` with a provider's signup token

The same call as after a code: `{ nickname, avatar?, language? }`, **201**
with a session and the `player`. The player is made with no phone, and the
pair is written to `identities` in the same transaction. The audit line
`signed_up` also names the `provider`.

- **409** `identity_taken`: the pair already has a player (two signups racing
  on one account). Checked where the phone's `phone_taken` is.

A player made this way has no `phone` in `/v1/me` (the field is left out).

## Settings

| Variable | |
| --- | --- |
| `GOOGLE_CLIENT_IDS` | Comma-separated client ids a Google token may name in `aud`. Empty: Google answers `501`. |
| `APPLE_CLIENT_IDS` | Comma-separated audiences for Apple: the bundle id, `app.gathering.jamaa`. Empty: Apple answers `501`. |

Neither is a secret. The provider's keys are fetched on the first token and
cached; a provider out of reach locks out its own players, nobody else.

## Deleting

Deleting a player deletes their identities (`ON DELETE CASCADE`). An Apple
player's deletion should also revoke the app's Apple token; there is no
token to revoke today, since the app never asks Apple for one.
