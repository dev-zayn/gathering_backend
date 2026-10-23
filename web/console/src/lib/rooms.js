// The rooms' closed lists, in the API's words, with the words each wears on a
// screen. The lists are the API's (docs/CONSOLE.md, docs/ROOMS.md); these are
// only the words.
export const STATUSES = ['open', 'lobby', 'playing', 'ended', 'all']

export const statusFilterLabel = {
  open: 'Open',
  lobby: 'In the lobby',
  playing: 'Playing',
  ended: 'Ended',
  all: 'All',
}

export const statusLabel = { lobby: 'In the lobby', playing: 'Playing', ended: 'Ended' }

export const statusTone = {
  lobby: 'bg-info/10 text-info',
  playing: 'bg-good/10 text-good',
  ended: 'bg-surface-2 text-ink-2',
}

// Why a room ended. Only the host's end names somebody; the others had nobody
// behind them, the console's included.
export const endReasonLabel = {
  host: 'Ended by its host',
  empty: 'Everybody left',
  idle: 'Sat idle in the lobby',
  timeout: 'Ran past its time limit',
  admin: 'Closed from the console',
  tournament: 'Its tournament match was decided',
}

export const eventLabel = {
  created: 'Made',
  joined: 'Joined',
  left: 'Left',
  kicked: 'Kicked',
  started: 'Round started',
  finished: 'Round finished',
  results: 'Results written',
  ended: 'Ended',
  host: 'New host',
}

export const roleLabel = { host: 'Host', player: 'Player', spectator: 'Spectator' }

// A round's number as people say it: no round yet, or the one being played or
// just finished.
export function roundLabel(room) {
  if (!room.round) return 'No round yet'
  return room.status === 'playing' ? `Round ${room.round}, playing` : `Round ${room.round}`
}
