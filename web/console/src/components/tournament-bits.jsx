import { statusLabel, statusTone } from '../lib/tournaments.js'

// The small pieces the two tournaments pages share: a tournament's status.

export function TournamentStatus({ status }) {
  return (
    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold whitespace-nowrap ${statusTone[status] ?? ''}`}>
      {statusLabel[status] ?? status}
    </span>
  )
}
