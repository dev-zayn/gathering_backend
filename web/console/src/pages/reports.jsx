import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Count, Pager } from '../components/list-bits.jsx'
import { when } from '../lib/games.js'
import { useMe } from '../lib/me.js'

const STATUSES = ['open', 'resolved', 'all']
const statusLabel = { open: 'Open', resolved: 'Dealt with', all: 'All' }
const reasonLabel = {
  name: 'Name, @handle or bio',
  photo: 'Photo',
  harassment: 'Harassment',
  cheating: 'Cheating',
  other: 'Something else',
}

// What players reported, newest first. The reported player is never told, and
// nothing happens to them by itself: an admin opens them on Players, switches
// them off if it comes to that, and marks the report dealt with here.
// docs/REPORTS.md is the contract.
export default function Reports() {
  const me = useMe()
  const queries = useQueryClient()
  const [params, setParams] = useSearchParams()
  const asked = params.get('status')
  const status = STATUSES.includes(asked) ? asked : 'open'
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]
  const [confirming, setConfirming] = useState(null)
  const [note, setNote] = useState(null)

  const list = useQuery({
    queryKey: ['reports', status, cursor],
    queryFn: () => api.get(`/v1/admin/reports?status=${status}&limit=50&cursor=${encodeURIComponent(cursor)}`),
    refetchInterval: 30_000,
  })
  const resolve = useMutation({
    mutationFn: (id) => api.post(`/v1/admin/reports/${id}/resolve`),
    onSuccess: () => {
      setNote({ tone: 'good', text: 'Marked as dealt with.' })
    },
    onError: (e) => setNote({ tone: 'bad', text: e.message }),
    onSettled: () => {
      setConfirming(null)
      queries.invalidateQueries({ queryKey: ['reports'] })
    },
  })

  function filter(next) {
    setParams(next === 'open' ? {} : { status: next })
    setCursors([''])
  }

  const canResolve = me?.roles?.includes('admin')
  const rows = list.data?.items ?? []
  return (
    <div>
      <h1 className="text-2xl font-extrabold">Reports</h1>
      <p className="text-ink-2 mt-1 text-sm">
        What players reported. The player is never told, and nothing happens to them until you act.
      </p>

      <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-3">
        <Count label="Open" value={list.data?.open} hint="Waiting for an answer" />
      </div>

      <div className="mt-5 flex flex-wrap items-center gap-2">
        {STATUSES.map((s) => (
          <button key={s} type="button" className="chip" aria-pressed={s === status} onClick={() => filter(s)}>
            {statusLabel[s]}
          </button>
        ))}
      </div>

      {note && <p className={`mt-3 text-sm ${note.tone === 'bad' ? 'text-bad' : 'text-good'}`}>{note.text}</p>}

      <div className="card mt-4 overflow-hidden">
        {list.isLoading && <p className="text-ink-3 px-4 py-6 text-sm">Loading…</p>}
        {list.isError && <p className="text-bad px-4 py-6 text-sm">{list.error.message}</p>}
        {list.data && rows.length === 0 && (
          <p className="text-ink-3 px-4 py-6 text-sm">{status === 'open' ? 'Nothing is waiting.' : 'No report matches.'}</p>
        )}
        {rows.map((r) => (
          <div key={r.id} className="border-line flex flex-wrap items-start gap-x-4 gap-y-2 border-b px-4 py-3 text-sm last:border-0">
            <div className="min-w-0 flex-1 basis-64">
              <div className="flex flex-wrap items-center gap-x-2">
                <span className="font-semibold">{r.player.nickname}</span>
                <span className="text-ink-2 font-mono text-xs" dir="ltr">
                  @{r.player.handle}
                </span>
                {r.player.disabled && <span className="text-bad text-xs">switched off</span>}
                {r.against > 1 && <span className="text-bad text-xs font-semibold">{r.against} open reports</span>}
              </div>
              <div className="mt-1 font-medium">{reasonLabel[r.reason] ?? r.reason}</div>
              {r.note && (
                <p className="text-ink-2 mt-1 break-words whitespace-pre-wrap" dir="auto">
                  {r.note}
                </p>
              )}
              <div className="text-ink-3 mt-1 text-xs">
                From {r.reporter.nickname}{' '}
                <span className="font-mono" dir="ltr">
                  @{r.reporter.handle}
                </span>{' '}
                · {when(r.created_at)}
                {r.status === 'resolved' && ` · dealt with by ${r.resolved_by ?? 'an admin'}, ${when(r.resolved_at)}`}
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Link to={`/players?q=${encodeURIComponent(r.player.handle)}`} className="btn btn-secondary h-8 px-3 text-xs">
                Open player
              </Link>
              {r.status === 'open' && (
                <button
                  type="button"
                  className="btn btn-primary h-8 px-3 text-xs"
                  disabled={!canResolve}
                  title={canResolve ? undefined : 'Only an admin can do this'}
                  onClick={() => {
                    setNote(null)
                    setConfirming(r)
                  }}
                >
                  Dealt with
                </button>
              )}
            </div>
          </div>
        ))}
      </div>
      <Pager
        cursors={cursors}
        next={list.data?.next_cursor}
        busy={list.isFetching}
        onOlder={(next) => setCursors([...cursors, next])}
        onNewer={() => setCursors(cursors.slice(0, -1))}
      />

      <Dialog
        open={Boolean(confirming)}
        onClose={() => !resolve.isPending && setConfirming(null)}
        title="Mark this report dealt with?"
        description="It leaves the open list. This does nothing to the player: switch them off on Players first if that is what you decided."
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setConfirming(null)} disabled={resolve.isPending}>
              Cancel
            </button>
            <button type="button" className="btn btn-primary" onClick={() => resolve.mutate(confirming.id)} disabled={resolve.isPending}>
              {resolve.isPending ? 'Saving…' : 'Yes, dealt with'}
            </button>
          </>
        }
      />
    </div>
  )
}
