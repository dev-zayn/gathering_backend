import { statusLabel, statusTone } from '../lib/rooms.js'

// The small pieces the two rooms pages share: a room's status, and the
// six-symbol code people read out to each other.

export function StatusBadge({ status }) {
  return (
    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold whitespace-nowrap ${statusTone[status] ?? ''}`}>
      {statusLabel[status] ?? status}
    </span>
  )
}

export function Code({ children, big = false }) {
  return (
    <span dir="ltr" className={`font-mono font-bold tracking-[0.2em] ${big ? 'text-2xl' : 'text-sm'}`}>
      {children}
    </span>
  )
}
