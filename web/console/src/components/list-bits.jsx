// The pieces a list page shares: the numbers above it, and its pages.

// A number at the top of a page: what it counts, and the count.
export function Count({ label, value, hint }) {
  return (
    <div className="card p-4">
      <div className="text-ink-2 text-xs font-semibold uppercase">{label}</div>
      <div className="mt-1 text-2xl font-light tabular-nums">{value ?? '—'}</div>
      {hint && <div className="text-ink-3 mt-0.5 text-xs">{hint}</div>}
    </div>
  )
}

// Older and newer pages of a list paged by cursor: the cursors walked so far
// are a stack, so going back needs no cursor from the API.
export function Pager({ cursors, next, onOlder, onNewer, busy }) {
  if (cursors.length <= 1 && !next) return null
  return (
    <div className="mt-3 flex items-center gap-2 text-sm">
      <button type="button" className="btn btn-secondary h-8 px-3 text-xs" disabled={cursors.length <= 1 || busy} onClick={onNewer}>
        Newer
      </button>
      <span className="text-ink-2">page {cursors.length}</span>
      <button type="button" className="btn btn-secondary h-8 px-3 text-xs" disabled={!next || busy} onClick={() => onOlder(next)}>
        Older
      </button>
    </div>
  )
}
