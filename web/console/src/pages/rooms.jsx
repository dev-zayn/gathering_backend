import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { ChevronRight } from 'lucide-react'

import { api } from '../api.js'
import { Tile } from '../components/game-bits.jsx'
import { Count, Pager } from '../components/list-bits.jsx'
import { Code, StatusBadge } from '../components/room-bits.jsx'
import { when } from '../lib/games.js'
import { endReasonLabel, roundLabel, STATUSES, statusFilterLabel } from '../lib/rooms.js'

// Every room, newest first: the open ones by default, any status or one game
// on asking. The counts at the top are the platform's whatever the filters
// say, and the page reads them again every few seconds while it is open.
export default function Rooms() {
  const [params, setParams] = useSearchParams()
  const asked = params.get('status')
  const status = STATUSES.includes(asked) ? asked : 'open'
  const game = params.get('game') ?? ''
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]

  const list = useQuery({
    queryKey: ['rooms', status, game, cursor],
    queryFn: () =>
      api.get(`/v1/admin/rooms?status=${status}&game=${encodeURIComponent(game)}&limit=50&cursor=${encodeURIComponent(cursor)}`),
    refetchInterval: 10_000,
  })
  const games = useQuery({ queryKey: ['games'], queryFn: () => api.get('/v1/admin/games') })

  function filter(next) {
    const merged = { status, game, ...next }
    const out = {}
    if (merged.status !== 'open') out.status = merged.status
    if (merged.game) out.game = merged.game
    setParams(out)
    setCursors([''])
  }

  const live = list.data?.live
  const rows = list.data?.items ?? []
  return (
    <div>
      <h1 className="text-xl font-extrabold">Rooms</h1>
      <p className="text-ink-2 mt-1 text-sm">Every room players made, who is in it, and how it ended.</p>

      <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-3">
        <Count label="Open now" value={live && live.lobby + live.playing} />
        <Count label="In the lobby" value={live?.lobby} hint="Waiting, or between rounds" />
        <Count label="Playing" value={live?.playing} hint="A round under way" />
      </div>

      <div className="mt-5 flex flex-wrap items-center gap-2">
        {STATUSES.map((s) => (
          <button
            key={s}
            type="button"
            className={`btn h-8 px-3 text-xs ${s === status ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => filter({ status: s })}
          >
            {statusFilterLabel[s]}
          </button>
        ))}
        <select
          className="field ms-auto h-8 w-full max-w-xs text-xs sm:w-auto"
          aria-label="Game"
          value={game}
          onChange={(e) => filter({ game: e.target.value })}
        >
          <option value="">Every game</option>
          {(games.data?.games ?? []).map((g) => (
            <option key={g.id} value={g.id}>
              {g.name} · {g.id}
            </option>
          ))}
        </select>
      </div>

      <div className="card mt-4 overflow-hidden">
        <div className="text-ink-2 border-line hidden grid-cols-[2fr_1fr_1.2fr_1.2fr_0.8fr_1fr_16px] gap-3 border-b px-4 py-2 text-xs font-semibold uppercase md:grid">
          <span>Room</span>
          <span>Status</span>
          <span>Round</span>
          <span>Host</span>
          <span className="text-end">In it</span>
          <span>Made</span>
          <span />
        </div>
        {list.isLoading && <p className="text-ink-3 px-4 py-6 text-sm">Loading…</p>}
        {list.isError && <p className="text-bad px-4 py-6 text-sm">{list.error.message}</p>}
        {list.data && rows.length === 0 && (
          <p className="text-ink-3 px-4 py-6 text-sm">
            {status === 'open' && !game ? 'No room is open right now.' : 'No room matches.'}
          </p>
        )}
        {rows.map((r) => (
          <Link
            key={r.id}
            to={`/rooms/${r.id}`}
            className="border-line hover:bg-surface-2/60 grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 border-b px-4 py-3 text-sm last:border-0 md:grid-cols-[2fr_1fr_1.2fr_1.2fr_0.8fr_1fr_16px] md:py-2"
          >
            <span className="flex min-w-0 items-center gap-3">
              <Tile src={r.game.icon} name={r.game.name} size="size-8" />
              <span className="min-w-0">
                <span className="block truncate font-semibold">{r.game.name || r.game.id}</span>
                <Code>{r.code}</Code>
              </span>
            </span>
            <span className="justify-self-end md:justify-self-start">
              <StatusBadge status={r.status} />
            </span>
            <span className="text-ink-2 text-xs">
              {roundLabel(r)}
              {r.end_reason && <span className="block">{endReasonLabel[r.end_reason] ?? r.end_reason}</span>}
            </span>
            <span className="min-w-0 truncate text-xs">
              <span className="font-semibold">{r.host.nickname || r.host.id}</span>{' '}
              <span className="text-ink-2 font-mono" dir="ltr">
                @{r.host.handle}
              </span>
            </span>
            <span className="text-xs tabular-nums md:text-end">
              {r.status === 'ended' ? (
                <span className="text-ink-2">{r.members} at the end</span>
              ) : (
                <>
                  <b>{r.members}</b> <span className="text-ink-2">in it, {r.connected} connected</span>
                </>
              )}
            </span>
            <span className="text-ink-2 text-xs whitespace-nowrap">{when(r.created_at)}</span>
            <ChevronRight className="text-ink-3 hidden size-4 md:block" />
          </Link>
        ))}
      </div>
      <Pager
        cursors={cursors}
        next={list.data?.next_cursor}
        busy={list.isFetching}
        onOlder={(next) => setCursors([...cursors, next])}
        onNewer={() => setCursors(cursors.slice(0, -1))}
      />
    </div>
  )
}
