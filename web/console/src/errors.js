// One sentence for every answer the API gives, so a screen never shows a code.
const sentences = {
  unauthorized: 'Sign in again.',
  wrong_password: 'That is not the console’s password.',
  forbidden: 'Your account cannot do that.',
  no_role: 'Your account has no role here. Ask for one.',
  admin_only: 'Only an admin can do that. Your account reads the console but cannot change it.',
  bad_origin: 'This page is not allowed to use your session.',
  not_found: 'Not found.',
  invalid_fields: 'Some fields need attention.',
  too_many_requests: 'Too many requests. Wait a moment.',
  identity_unavailable: 'The identity provider is not answering.',
  internal_error: 'Something went wrong on our side.',
  not_supported: 'Not available yet.',
  room_closed: 'This room has already ended.',
  report_resolved: 'Somebody already dealt with this report.',
  tournament_closed: 'This tournament is already over.',
  tournament_started: 'This tournament has already started.',
  tournament_not_running: 'This tournament has not started yet.',
  too_few_entrants: 'Not enough entrants to start it yet.',
  not_knockout: 'Only a knockout match can be decided this way.',
  not_league: 'Only a league can be finished early.',
  nothing_played: 'No round has been counted yet. Call it off instead.',
  match_waiting: 'That match is still waiting for a side.',
  match_decided: 'That match is already decided.',
  // Publishers (docs/PUBLISHERS.md).
  wrong_token: 'This page is for another kind of account. Sign out and sign in again.',
  publisher_disabled: 'This studio’s account is switched off.',
  invalid_setup: 'This link has been used or has expired. Ask for a new one.',
  email_taken: 'Another publisher signs in with that email.',
  already_registered: 'A game with that id is already registered.',
  build_open: 'A version is already on its way. Publish or discard it first.',
  suspended: 'The game is suspended.',
  in_review: 'It is in review: take it back to change it.',
  not_open: 'That version is already published or discarded.',
  not_submittable: 'That version is already in review or approved.',
  not_in_review: 'That version is not in review any more.',
  not_approved: 'Only an approved version can be published.',
  testers_only: 'Only the game’s testers can do that.',
}

// A field's problem, said in words. The key is `field:problem` as the API
// names them; anything not here is shown as the API wrote it.
const fieldSentences = {
  'status:unknown': 'Pick a status from the list.',
  'limit:1_to_100': 'A page shows 1 to 100 rows.',
  'cursor:invalid': 'That page is no longer there. Start from the first page.',
  'title:required': 'Write a title.',
  'title:too_long': 'The title is at most 60 characters.',
  'body:too_long': 'The body is at most 240 characters.',
  'game:not_found': 'That game is not in the registry.',
  'audience:required': 'Choose who hears it.',
  'audience.kind:unknown': 'Choose everyone, a game’s players, or chosen players.',
  'audience.game:not_found': 'Pick the game whose players hear it.',
  'audience.players:required': 'Name at least one player, by @handle or id.',
  'audience.players:up_to_500': 'At most 500 players at once.',
  'audience.players:nobody': 'None of those players can hear it: nobody by those names, or every one of them is switched off.',
  'game:not_multiplayer': 'That game seats one player; a tournament needs two at a table.',
  'name:required': 'Give it a name.',
  'name:too_long': 'The name is at most 40 characters.',
  'format:unknown': 'Pick knockout or league.',
  'format:needs_duels': 'This game needs more than two at a table: make it a league.',
  'best_of:1_3_or_5': 'A match is one round, best of 3 or best of 5.',
  'rounds:1_to_20': 'A league plays 1 to 20 rounds.',
  'size:out_of_range': 'That many entrants will not fit: a knockout takes 2 to 64, a league the game’s table.',
  'starts_at:in_the_past': 'Pick a start time in the future.',
  'player:not_in_match': 'That player is not in this match.',
  'email:required': 'Write the email.',
  'email:invalid': 'That is not an email address.',
  'email:too_long': 'The email is at most 254 characters.',
  'password:required': 'Write the password.',
  'password:too_short': 'The password needs at least 10 characters.',
  'password:too_long': 'The password is at most 72 bytes.',
  'url:must be an http or https URL': 'Give the manifest’s full address, starting with https://.',
  'id:another_game': 'That manifest names another game.',
  'version:same_as_live': 'That manifest is at the live version: bump its version first.',
  'tagline:too_long': 'The tagline is at most 160 characters.',
  'description:too_long': 'The description is at most 4000 characters.',
  'category:unknown': 'Pick a category from the list.',
  'notes:too_long': 'The notes are at most 2000 characters.',
  'note:required': 'Say what to fix.',
  'note:too_long': 'The note is at most 2000 characters.',
  'label:too_long': 'The label is at most 60 characters.',
  'max_uses:1_to_1000': 'A code takes 1 to 1000 testers, or no limit.',
  'days:1_to_90': 'A code works 1 to 90 days.',
  'invites:too_many': 'This game has 20 live codes. Stop one first.',
}

export function fieldSentence(field, problem) {
  return fieldSentences[`${field}:${problem}`] ?? `${field}: ${problem.replaceAll('_', ' ')}`
}

export function explain(data, status) {
  const code = data?.error
  if (code === 'invalid_fields' && data.fields) {
    return Object.entries(data.fields)
      .map(([k, v]) => fieldSentence(k, v))
      .join(' ')
  }
  if (code === 'incomplete' && data.missing) return `Missing: ${data.missing.join(', ')}`
  if (code === 'too_many_requests' && data.retry_after) return `Too many requests. Try again in ${data.retry_after}s.`
  if (code && sentences[code]) return sentences[code]
  if (code) return code.replaceAll('_', ' ')
  if (status === 401) return sentences.unauthorized
  if (status === 403) return sentences.forbidden
  if (status === 404) return sentences.not_found
  if (status >= 500) return sentences.internal_error
  return `Request failed (${status})`
}
