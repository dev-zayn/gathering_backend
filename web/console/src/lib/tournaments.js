// The tournaments' closed lists, in the API's words, with the words each
// wears on a screen. The lists are the API's (docs/TOURNAMENTS.md); these are
// only the words.
export const STATUSES = ['live', 'open', 'running', 'done', 'cancelled', 'all']

export const statusFilterLabel = {
  live: 'Live',
  open: 'Taking entrants',
  running: 'Being played',
  done: 'Done',
  cancelled: 'Called off',
  all: 'All',
}

export const statusLabel = { open: 'Taking entrants', running: 'Being played', done: 'Done', cancelled: 'Called off' }

export const statusTone = {
  open: 'bg-info/10 text-info',
  running: 'bg-good/10 text-good',
  done: 'bg-surface-2 text-ink-2',
  cancelled: 'bg-bad/10 text-bad',
}

export const formatLabel = { knockout: 'Knockout', league: 'League' }

export const visibilityLabel = { private: 'Anybody with the code', friends: "The organizer's friends", public: 'Official, listed for everybody' }

// Why a tournament is over.
export const endReasonLabel = {
  played: 'Played to the end',
  early: 'League finished early',
  withdrawals: 'Fewer than two were left',
  organizer: 'Called off by its organizer',
  admin: 'Called off from the console',
  too_few: 'Too few entrants at its start time',
  expired: 'Sat open too long',
}

// How a match was decided.
export const decidedLabel = {
  play: 'Played',
  bye: 'A bye',
  walkover: 'Walkover',
  award: 'Awarded',
  standings: 'By the table',
}

export const matchStatusLabel = { waiting: 'Waiting for a side', ready: 'Ready to play', done: 'Decided' }

export const entrantStatusLabel = { in: 'In', out: 'Out', withdrawn: 'Withdrew' }

// A knockout stage as people say it: the final, the semi-finals, the
// quarter-finals, else its number.
export function stageLabel(stage, stages) {
  switch (stages - stage) {
    case 0:
      return 'Final'
    case 1:
      return 'Semi-finals'
    case 2:
      return 'Quarter-finals'
    default:
      return `Stage ${stage}`
  }
}

// The length of a tournament's matches, as one line.
export function lengthLabel(t) {
  if (t.format === 'league') return `${t.rounds} ${t.rounds === 1 ? 'round' : 'rounds'}`
  return t.best_of === 1 ? 'One round a match' : `Best of ${t.best_of}`
}
