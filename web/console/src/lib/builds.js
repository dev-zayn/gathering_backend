// A build's way to players (docs/PUBLISHERS.md), in the words a screen uses.
// The statuses are the API's; these are only their labels and tones.

export const buildLabel = {
  testing: 'Testing',
  in_review: 'In review',
  approved: 'Approved',
  rejected: 'Changes asked',
  published: 'Published',
  withdrawn: 'Withdrawn',
}

export const buildTone = {
  testing: 'bg-accent/15 text-accent-text',
  in_review: 'bg-warn/10 text-warn',
  approved: 'bg-good/10 text-good',
  rejected: 'bg-bad/10 text-bad',
  published: 'bg-surface-2 text-ink-2',
  withdrawn: 'bg-surface-2 text-ink-3',
}

// The four steps an open build walks, in order. A rejected build stands where
// a tested one does: it goes back to review from there.
export const STEPS = [
  ['testing', 'Testing'],
  ['in_review', 'In review'],
  ['approved', 'Approved'],
  ['published', 'Published'],
]

export function stepOf(status) {
  if (status === 'rejected') return 0
  const i = STEPS.findIndex(([s]) => s === status)
  return i < 0 ? 0 : i
}

// What a build would change about the live game, by the API's names.
export const changeLabel = {
  name: 'Name',
  entry: 'Entry',
  scopes: 'Scopes',
  permissions: 'Permissions',
  room: 'Room',
  orientation: 'Orientation',
  splash: 'Splash',
  tagline: 'Tagline',
  description: 'Description',
  category: 'Category',
  icon: 'Icon',
  screenshots: 'Screenshots',
}

// A code reads in two halves, the way it is said aloud.
export const spaced = (code) => (code?.length === 8 ? `${code.slice(0, 4)}-${code.slice(4)}` : code)

// Whether a code still makes testers.
export function inviteLive(i) {
  if (i.revoked_at) return false
  if (new Date(i.expires_at).getTime() <= Date.now()) return false
  return i.max_uses === 0 || i.uses < i.max_uses
}

export function inviteState(i) {
  if (i.revoked_at) return 'Revoked'
  if (new Date(i.expires_at).getTime() <= Date.now()) return 'Expired'
  if (i.max_uses > 0 && i.uses >= i.max_uses) return 'Used up'
  return 'Live'
}

// A publisher account's status, as the console colours it.
export const publisherTone = {
  invited: 'bg-warn/10 text-warn',
  active: 'bg-good/10 text-good',
  disabled: 'bg-bad/10 text-bad',
}
