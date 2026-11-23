import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

import { Fact, Note } from './game-bits.jsx'
import { buildLabel, buildTone, changeLabel, STEPS, spaced, stepOf } from '../lib/builds.js'
import { roomFacts, when } from '../lib/games.js'

// The pieces the publisher's game page and the reviewer's build page share
// (docs/PUBLISHERS.md): a status badge, the steps, what the manifest says,
// what changes, the reviewer's words, and a code to copy.

export function BuildBadge({ status }) {
  return (
    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold ${buildTone[status] ?? ''}`}>
      {buildLabel[status] ?? status}
    </span>
  )
}

// Where an open build stands: four steps, the current one lit, a rejected one
// standing at the first with the reviewer's words beside it.
export function Steps({ status }) {
  const at = stepOf(status)
  return (
    <ol className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs" aria-label="Where this build is">
      {STEPS.map(([key, label], i) => {
        const done = i < at
        const here = i === at
        return (
          <li key={key} className="flex items-center gap-2">
            <span
              className={`inline-flex size-5 items-center justify-center rounded-full font-mono text-[10px] font-bold ${
                here
                  ? status === 'rejected'
                    ? 'bg-bad text-white'
                    : 'bg-accent text-accent-ink'
                  : done
                    ? 'bg-good/20 text-good'
                    : 'bg-surface-2 text-ink-3'
              }`}
              aria-current={here ? 'step' : undefined}
            >
              {done ? <Check className="size-3" /> : i + 1}
            </span>
            <span className={here ? 'font-semibold' : 'text-ink-2'}>
              {here && status === 'rejected' ? 'Changes asked' : label}
            </span>
            {i < STEPS.length - 1 && <span className="bg-line hidden h-px w-6 sm:block" />}
          </li>
        )
      })}
    </ol>
  )
}

// What the build's manifest says: read from the document, never typed.
export function ManifestFacts({ build: b }) {
  return (
    <div className="grid gap-3">
      <div className="grid grid-cols-2 gap-3">
        <Fact label="Version" mono>
          {b.version}
        </Fact>
        <Fact label="Last read">{when(b.fetched_at)}</Fact>
        <Fact label="Orientation">{b.orientation}</Fact>
        <Fact label="Splash">{b.splash ? 'Covered until ready' : 'No'}</Fact>
      </div>
      <Fact label="Manifest" mono>
        <a href={b.manifest_url} target="_blank" rel="noopener noreferrer" className="hover:underline" dir="ltr">
          {b.manifest_url}
        </a>
      </Fact>
      <Fact label="Entry" mono>
        <a href={b.entry} target="_blank" rel="noopener noreferrer" className="hover:underline" dir="ltr">
          {b.entry}
        </a>
      </Fact>
      <Fact label="Scopes">{b.scopes?.length ? b.scopes.join(', ') : 'Identity and nothing more.'}</Fact>
      <Fact label="Permissions">{b.permissions?.length ? b.permissions.join(', ') : 'None.'}</Fact>
    </div>
  )
}

export function RoomFacts({ room }) {
  return (
    <div className="grid grid-cols-2 gap-3">
      {roomFacts(room).map(([label, value]) => (
        <Fact key={label} label={label}>
          {value}
        </Fact>
      ))}
    </div>
  )
}

// What publishing it would change about the live game.
export function Changes({ build: b, live }) {
  if (!live) return <p className="text-ink-2 text-sm">The first version: everything in it is new to players.</p>
  if (!b.changes?.length) return <p className="text-ink-2 text-sm">Nothing a player sees differs from the live game.</p>
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap gap-1.5">
        {b.changes.map((c) => (
          <span key={c} className="bg-warn/10 text-warn inline-flex rounded-full px-2 py-0.5 text-xs font-semibold">
            {changeLabel[c] ?? c}
          </span>
        ))}
      </div>
      {b.new_scopes?.length > 0 && (
        <Note tone="wait">
          New scopes the live game never asked for: <b>{b.new_scopes.join(', ')}</b>.
        </Note>
      )}
    </div>
  )
}

// The reviewer's words on the last decision.
export function ReviewWords({ build: b }) {
  if (!b.review) return null
  const rejected = b.status === 'rejected'
  return (
    <Note tone={rejected ? 'bad' : 'good'}>
      <b>{rejected ? 'The reviewer asked for changes' : 'Approved'}</b>
      <span className="text-ink-2"> · {when(b.review.at)}</span>
      {b.review.note ? (
        <p className="text-ink mt-1 whitespace-pre-wrap">{b.review.note}</p>
      ) : (
        !rejected && <p className="text-ink-2 mt-1">Publish it whenever you are ready.</p>
      )}
    </Note>
  )
}

// A tester's code, read in two halves, with the key to copy it.
export function CodeChip({ code, muted = false }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(spaced(code))
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }
  return (
    <span className="inline-flex items-center gap-1.5">
      <code className={`font-mono text-sm font-semibold tracking-wider ${muted ? 'text-ink-3 line-through' : ''}`} dir="ltr">
        {spaced(code)}
      </code>
      {!muted && (
        <button type="button" className="text-ink-3 hover:text-ink" aria-label={`Copy ${spaced(code)}`} onClick={copy}>
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
        </button>
      )}
    </span>
  )
}
