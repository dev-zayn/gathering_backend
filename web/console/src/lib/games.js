// The registry's closed lists, in the API's words, with the label each wears
// on a screen. The lists themselves are the API's; these are only the words.
export const LISTINGS = ['all', 'published', 'unpublished', 'suspended']

export const categoryLabel = {
  speed: 'Speed',
  brain: 'Brain',
  words: 'Words',
  luck: 'Luck',
  guess: 'Guess',
  draw: 'Draw',
  party: 'Party',
  other: 'Other',
}

export const tierLabel = { 'first-party': 'Ours', partner: 'A studio' }

export const listingTone = {
  published: 'bg-good/10 text-good',
  unpublished: 'bg-surface-2 text-ink-2',
  suspended: 'bg-bad/10 text-bad',
}

// Publishing is refused without these, so a row says which is absent rather
// than making somebody open the game to find out.
export function needs(g) {
  if (!g.tagline && !g.icon) return 'Needs a tagline and an icon'
  if (!g.tagline) return 'Needs a tagline'
  if (!g.icon) return 'Needs an icon'
  return null
}

// A manifest is re-read about once a day; one older than this is a fetch
// that is failing.
const STALE = 2 * 24 * 60 * 60 * 1000
export const overdue = (at) => Boolean(at) && Date.now() - new Date(at).getTime() > STALE

export const when = (at) => (at ? at.slice(0, 16).replace('T', ' ') : 'never')

// The room block, as facts a person reads.
export function roomFacts(room) {
  if (!room) return []
  return [
    ['Players', room.min === room.max ? String(room.min) : `${room.min} to ${room.max}`],
    ['Host', room.host === 'authoritative' ? 'Authoritative — the host writes the state' : 'Peer — any member writes'],
    ['Pace', room.pace === 'turn' ? 'In turns' : 'Realtime'],
    ['State', room.state === 'none' ? 'None — no shared state' : 'Blob — a versioned shared state'],
    ['On a Wi-Fi with no internet', room.lan ? 'Yes' : 'No'],
    ['Back in during a round', room.rejoin ? 'Yes, for a member who left' : 'No'],
    ['Spectators', room.spectators ? 'Allowed' : 'No'],
  ]
}
