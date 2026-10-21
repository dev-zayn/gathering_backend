import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

import { listingTone } from '../lib/games.js'

// The small pieces the two games pages share: a tile, a listing badge, a
// secret shown once, a labelled fact.

export function Tile({ src, name, size = 'size-10' }) {
  if (src) return <img src={src} alt="" className={`${size} shrink-0 rounded-lg object-cover`} />
  return (
    <span className={`bg-surface-2 text-ink-2 inline-flex ${size} shrink-0 items-center justify-center rounded-lg text-sm font-bold`}>
      {(name || '?').slice(0, 1)}
    </span>
  )
}

export function ListingBadge({ listing }) {
  return (
    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold ${listingTone[listing] ?? ''}`}>
      {listing}
    </span>
  )
}

export function Tag({ children, tone = 'bg-surface-2 text-ink-2' }) {
  return <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold ${tone}`}>{children}</span>
}

// A value that is read, not edited.
export function Fact({ label, children, mono = false }) {
  return (
    <div className="min-w-0">
      <div className="text-ink-3 text-xs font-semibold uppercase">{label}</div>
      <div className={`mt-0.5 text-sm [overflow-wrap:anywhere] ${mono ? 'font-mono text-xs' : ''}`}>
        {children || <span className="text-ink-3">—</span>}
      </div>
    </div>
  )
}

// A note on the page: what is waiting, what went wrong, what just happened.
const noteTone = {
  good: 'border-good/40 bg-good/5',
  bad: 'border-bad/40 bg-bad/5 text-bad',
  wait: 'border-warn/40 bg-warn/5',
}

export function Note({ tone = 'wait', children }) {
  return <div className={`rounded-lg border p-3 text-sm ${noteTone[tone] ?? noteTone.wait}`}>{children}</div>
}

// The client secret, shown once and never again. Copying it is the whole
// point, so the button is the biggest thing on it.
export function SecretOnce({ secret }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(secret)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }
  return (
    <Note tone="wait">
      <b>Client secret — shown once.</b>
      <div className="mt-2 flex items-center gap-2">
        <code className="bg-surface min-w-0 flex-1 rounded-md border border-line px-2 py-1.5 font-mono text-xs [overflow-wrap:anywhere]" dir="ltr">
          {secret}
        </code>
        <button type="button" className="btn btn-secondary h-8 px-3 text-xs" onClick={copy}>
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <p className="text-ink-2 mt-2 text-xs">Send it to the studio now. It cannot be read back, only rolled again.</p>
    </Note>
  )
}
