import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, Check, Plus, Undo2 } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { BuildBadge, Changes, CodeChip, ManifestFacts, ReviewWords, RoomFacts, Steps } from '../components/build-bits.jsx'
import { Fact, ListingBadge, Note, Tile } from '../components/game-bits.jsx'
import { categoryLabel, when } from '../lib/games.js'
import { useMe } from '../lib/me.js'

// One build as the reviewer reads it (docs/PUBLISHERS.md): what its manifest
// asks for beside the live game, the store copy and pictures it would carry,
// the publisher's notes, codes to play it with in the app, and the two
// answers.
export default function Build() {
  const { id } = useParams()
  const me = useMe()
  const canWrite = me?.roles?.includes('admin')
  const queries = useQueryClient()
  const page = useQuery({ queryKey: ['build', id], queryFn: () => api.get(`/v1/admin/builds/${id}`) })
  const [deciding, setDeciding] = useState(null)
  const [note, setNote] = useState('')
  const decide = useMutation({
    mutationFn: ({ to, words }) => api.post(`/v1/admin/builds/${id}/${to}`, words ? { note: words } : {}),
    onSuccess: (r) => {
      queries.setQueryData(['build', id], r)
      queries.invalidateQueries({ queryKey: ['review'] })
      setDeciding(null)
      setNote('')
    },
  })
  const invite = useMutation({
    mutationFn: () => api.post(`/v1/admin/builds/${id}/invite`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['build', id] }),
  })

  if (page.isLoading) return <p className="text-ink-3 text-sm">Loading…</p>
  if (page.isError)
    return (
      <div>
        <p className="text-bad text-sm">{page.error.message}</p>
        <Link to="/review" className="btn btn-secondary mt-4">
          <ArrowLeft className="size-4" /> Review
        </Link>
      </div>
    )
  const { build: b, game: g, publisher: p, invites } = page.data
  const live = g.listing !== 'unpublished'
  const waiting = b.status === 'in_review'

  return (
    <div className="grid gap-5">
      <div>
        <Link to="/review" className="text-ink-2 hover:text-ink inline-flex items-center gap-1 text-xs font-semibold">
          <ArrowLeft className="size-3.5" /> Review
        </Link>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="flex items-center gap-4">
            <Tile src={b.store.icon} name={b.name} size="size-16" />
            <div>
              <h1 className="text-2xl font-extrabold">
                {b.name} <span className="font-mono text-lg">{b.version}</span>
              </h1>
              <div className="text-ink-2 mt-0.5 flex flex-wrap items-center gap-2 text-sm">
                <Link to={`/games/${g.id}`} className="font-mono text-xs hover:underline" dir="ltr">
                  {g.id}
                </Link>
                <BuildBadge status={b.status} />
                {p && (
                  <Link to={`/publishers/${p.id}`} className="text-xs hover:underline">
                    by {p.name}
                  </Link>
                )}
              </div>
            </div>
          </div>
          {waiting && (
            <div className="flex flex-wrap gap-2">
              <button type="button" className="btn btn-secondary" disabled={!canWrite} onClick={() => setDeciding('reject')}>
                <Undo2 className="size-4" /> Ask for changes
              </button>
              <button type="button" className="btn btn-primary" disabled={!canWrite} onClick={() => setDeciding('approve')}>
                <Check className="size-4" /> Approve
              </button>
            </div>
          )}
        </div>
      </div>

      <Steps status={b.status} />
      <ReviewWords build={b} />
      {!waiting && b.status !== 'approved' && b.status !== 'rejected' && (
        <Note tone="wait">Not in review: it is {b.status.replace('_', ' ')}.</Note>
      )}
      {b.notes && (
        <Note tone="wait">
          <b>The publisher’s notes</b>
          <p className="text-ink mt-1 whitespace-pre-wrap">{b.notes}</p>
        </Note>
      )}

      <div className="grid gap-5 lg:grid-cols-3">
        <section className="card grid content-start gap-3 p-4">
          <h2 className="text-sm font-extrabold">What its manifest asks for</h2>
          <ManifestFacts build={b} />
        </section>
        <section className="card grid content-start gap-3 p-4">
          <h2 className="text-sm font-extrabold">The room</h2>
          <RoomFacts room={b.room} />
        </section>
        <section className="card grid content-start gap-3 p-4">
          <h2 className="text-sm font-extrabold">Beside the live game</h2>
          <div className="flex items-center gap-2 text-sm">
            <ListingBadge listing={g.listing} />
            {g.version && <span className="font-mono text-xs">live {g.version}</span>}
          </div>
          <Changes build={b} live={live} />
        </section>
      </div>

      <div className="grid gap-5 lg:grid-cols-3">
        <section className="card grid content-start gap-3 p-4 lg:col-span-2">
          <h2 className="text-sm font-extrabold">The store copy it would carry</h2>
          <Fact label="Tagline">{b.store.tagline}</Fact>
          <Fact label="Category">{categoryLabel[b.store.category] ?? b.store.category}</Fact>
          <Fact label="Description">
            {b.store.description && <span className="whitespace-pre-wrap">{b.store.description}</span>}
          </Fact>
          <div>
            <div className="label">Screenshots</div>
            <div className="mt-2 flex flex-wrap gap-2">
              {b.store.screenshots.length === 0 && <span className="text-ink-3 text-sm">None.</span>}
              {b.store.screenshots.map((s) => (
                <a key={s.id} href={s.url} target="_blank" rel="noopener noreferrer">
                  <img src={s.url} alt="" className="h-36 w-auto rounded-lg object-cover" />
                </a>
              ))}
            </div>
          </div>
        </section>

        <section className="card grid content-start gap-3 p-4">
          <h2 className="text-sm font-extrabold">Play it</h2>
          <p className="text-ink-2 text-sm">
            In the app, as a player: Me → Beta games, then one of these codes. Opening the entry in a browser shows the
            page alone, without the app around it.
          </p>
          {invites.length === 0 ? (
            <p className="text-ink-3 text-sm">The game has no live code.</p>
          ) : (
            <ul className="grid gap-2">
              {invites.map((i) => (
                <li key={i.code} className="flex flex-wrap items-center justify-between gap-2">
                  <CodeChip code={i.code} />
                  <span className="text-ink-2 text-xs">
                    {i.label || 'No label'} · until {when(i.expires_at).slice(0, 10)}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <div>
            <button type="button" className="btn btn-secondary h-8 px-3 text-xs" disabled={!canWrite || invite.isPending} onClick={() => invite.mutate()}>
              <Plus className="size-3.5" /> {invite.isPending ? 'Making…' : 'A code for me'}
            </button>
            {invite.isError && <p className="text-bad mt-2 text-xs">{invite.error.message}</p>}
          </div>
        </section>
      </div>

      <Dialog
        open={Boolean(deciding)}
        onClose={() => !decide.isPending && setDeciding(null)}
        title={deciding === 'approve' ? `Approve ${b.name} ${b.version}?` : `Ask for changes to ${b.name} ${b.version}`}
        description={
          deciding === 'approve'
            ? 'The publisher can then publish it: every player gets it from their next open.'
            : 'It goes back to the publisher with your words. Say what to fix.'
        }
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setDeciding(null)} disabled={decide.isPending}>
              Cancel
            </button>
            <button
              type="submit"
              form="decide-build"
              className="btn btn-primary"
              disabled={decide.isPending || (deciding === 'reject' && !note.trim())}
            >
              {decide.isPending ? 'Sending…' : deciding === 'approve' ? 'Approve' : 'Send it back'}
            </button>
          </>
        }
      >
        <form
          id="decide-build"
          className="grid gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            decide.mutate({ to: deciding, words: note.trim() })
          }}
        >
          <label className="grid gap-1 text-sm font-semibold">
            {deciding === 'approve' ? 'A note for the publisher (optional)' : 'What to fix'}
            <textarea
              className="field h-auto py-2 font-normal"
              rows={5}
              maxLength={2000}
              autoFocus
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </label>
          {decide.isError && <p className="text-bad text-sm">{decide.error.message}</p>}
        </form>
      </Dialog>
    </div>
  )
}
