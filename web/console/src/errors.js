// One sentence for every answer the API gives, so a screen never shows a code.
const sentences = {
  unauthorized: 'Sign in again.',
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
  tournament_closed: 'This tournament is already over.',
  tournament_started: 'This tournament has already started.',
  tournament_not_running: 'This tournament has not started yet.',
  too_few_entrants: 'Not enough entrants to start it yet.',
  not_knockout: 'Only a knockout match can be decided this way.',
  not_league: 'Only a league can be finished early.',
  nothing_played: 'No round has been counted yet. Call it off instead.',
  match_waiting: 'That match is still waiting for a side.',
  match_decided: 'That match is already decided.',
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
