import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Fact } from '../components/game-bits.jsx'
import { Code } from '../components/room-bits.jsx'
import { when } from '../lib/games.js'
import { useMe } from '../lib/me.js'

const presenceTone = { online: 'text-good', in_room: 'text-info', offline: 'text-ink-3' }
const presenceLabel = { online: 'online', in_room: 'in a room', offline: 'offline' }

// Players: find by handle, open one's record (friends, rounds played, the
// room they are in, their blocks both ways), switch one off or on.
export default function Players() {
  // A report links here with the player's handle already typed.
  const [params] = useSearchParams()
  const [q, setQ] = useState(params.get('q') ?? '')
  const [open, setOpen] = useState(null)
  const queries = useQueryClient()
  const list = useQuery({
    queryKey: ['players', q],
    queryFn: () => api.get(`/v1/admin/players?q=${encodeURIComponent(q)}&limit=100`),
  })
  const flip = useMutation({
    mutationFn: ({ id, disabled }) => api.post(`/v1/admin/players/${id}/${disabled ? 'enable' : 'disable'}`),
    onSuccess: () => {
      queries.invalidateQueries({ queryKey: ['players'] })
      queries.invalidateQueries({ queryKey: ['player'] })
    },
  })

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-extrabold">Players</h1>
        <input
          className="field max-w-xs"
          placeholder="@handle"
          value={q}
          onChange={(e) => setQ(e.target.value.trim())}
          aria-label="Find by handle"
        />
      </div>
      {flip.isError && <p className="text-bad mt-3 text-sm">{flip.error.message}</p>}
      <div className="card mt-4 overflow-x-auto">
        <table className="w-full min-w-[640px] text-sm">
          <thead className="text-ink-3 border-line bg-surface-2/60 border-b text-left text-xs">
            <tr>
              <th className="px-4 py-2">Player</th>
              <th className="px-4 py-2">Handle</th>
              <th className="px-4 py-2">Phone</th>
              <th className="px-4 py-2">Presence</th>
              <th className="px-4 py-2">Since</th>
              <th className="px-4 py-2 text-right">State</th>
            </tr>
          </thead>
          <tbody>
            {list.isLoading && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={6}>
                  Loading…
                </td>
              </tr>
            )}
            {list.isError && (
              <tr>
                <td className="text-bad px-4 py-6" colSpan={6}>
                  {list.error.message}
                </td>
              </tr>
            )}
            {list.data?.players?.length === 0 && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={6}>
                  Nobody matches.
                </td>
              </tr>
            )}
            {list.data?.players?.map((p) => (
              <tr key={p.id} className="border-line border-b last:border-0">
                <td className="px-4 py-2 font-semibold">
                  <button type="button" className="hover:underline" onClick={() => setOpen(p.id)}>
                    {p.nickname}
                  </button>
                </td>
                <td className="px-4 py-2 font-mono text-xs">@{p.handle}</td>
                <td className="px-4 py-2 font-mono text-xs" dir="ltr">
                  {p.phone || '—'}
                </td>
                <td className={`px-4 py-2 text-xs font-semibold ${presenceTone[p.presence] ?? ''}`}>
                  {presenceLabel[p.presence] ?? p.presence}
                </td>
                <td className="text-ink-2 px-4 py-2 text-xs">{p.created_at?.slice(0, 10)}</td>
                <td className="px-4 py-2 text-right">
                  <button
                    type="button"
                    className="btn btn-secondary h-8 px-3 text-xs"
                    disabled={flip.isPending}
                    onClick={() => flip.mutate({ id: p.id, disabled: p.disabled })}
                  >
                    {p.disabled ? 'Enable' : 'Disable'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Record id={open} onClose={() => setOpen(null)} flip={flip} />
    </div>
  )
}

// One player's record, read when it opens: who they are, how far they have
// got, and where they are now.
function Record({ id, onClose, flip }) {
  const p = useQuery({
    queryKey: ['player', id],
    queryFn: () => api.get(`/v1/admin/players/${id}`),
    enabled: Boolean(id),
  })
  const r = p.data
  return (
    <Dialog
      open={Boolean(id)}
      onClose={onClose}
      title={r ? r.nickname : 'Player'}
      description={r ? `@${r.handle}${r.disabled ? ' · switched off' : ''}` : undefined}
      footer={
        r && (
          <>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Done
            </button>
            <button
              type="button"
              className="btn btn-primary"
              disabled={flip.isPending}
              onClick={() => flip.mutate({ id: r.id, disabled: r.disabled })}
            >
              {r.disabled ? 'Switch back on' : 'Switch off'}
            </button>
          </>
        )
      }
    >
      {p.isLoading && <p className="text-ink-3 text-sm">Loading…</p>}
      {p.isError && <p className="text-bad text-sm">{p.error.message}</p>}
      {r && (
        <div className="grid gap-4">
          <div className="grid grid-cols-3 gap-3">
            <Stat label="Friends" value={r.friends} />
            <Stat label="Rounds played" value={r.rounds} />
            <div className="bg-surface-2 rounded-lg p-3">
              <div className="label">In a room</div>
              <div className="mt-1 text-sm">
                {r.room ? (
                  <Link to={`/rooms/${r.room.id}`} className="hover:underline" onClick={onClose}>
                    <Code>{r.room.code}</Code>
                  </Link>
                ) : (
                  <span className="text-ink-3">No</span>
                )}
              </div>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Fact label="Presence">{presenceLabel[r.presence] ?? r.presence}</Fact>
            <Fact label="Bio">{r.bio || '—'}</Fact>
            <Fact label="Language">{r.language}</Fact>
            <Fact label="Phone" mono>
              {r.phone}
            </Fact>
            <Fact label="Signed up">{when(r.created_at)}</Fact>
            <Fact label="Last seen">{r.last_seen_at ? when(r.last_seen_at) : null}</Fact>
            <Fact label="ID" mono>
              {r.id}
            </Fact>
          </div>
          <Blocks player={r} />
        </div>
      )}
    </Dialog>
  )
}

// A player's blocks both ways (docs/CONSOLE.md, "A player's blocks"): who
// they blocked and who blocked them, each one liftable behind a question.
function Blocks({ player }) {
  const me = useMe()
  const canLift = Boolean(me?.roles?.includes('admin'))
  const queries = useQueryClient()
  const blocks = useQuery({
    queryKey: ['player-blocks', player.id],
    queryFn: () => api.get(`/v1/admin/players/${player.id}/blocks`),
  })
  const lift = useMutation({
    mutationFn: ({ blocker, blocked }) => api.del(`/v1/admin/players/${blocker}/blocks/${blocked}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['player-blocks'] }),
  })
  const ask = (blocker, blocked) => {
    if (
      confirm(
        `Lift ${blocker.nickname}'s block of ${blocked.nickname}? Neither is told, and nothing the block ended comes back.`
      )
    )
      lift.mutate({ blocker: blocker.id, blocked: blocked.id })
  }

  if (blocks.isLoading) return <p className="text-ink-3 text-sm">Loading blocks…</p>
  if (blocks.isError) return <p className="text-bad text-sm">{blocks.error.message}</p>
  const b = blocks.data
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <BlockList
        label="Blocked"
        empty="Has blocked nobody."
        rows={b.blocked}
        busy={lift.isPending}
        canLift={canLift}
        onLift={(row) => ask(player, row)}
      />
      <BlockList
        label="Blocked by"
        empty="Nobody has blocked them."
        rows={b.blocked_by}
        busy={lift.isPending}
        canLift={canLift}
        onLift={(row) => ask(row, player)}
      />
      {lift.isError && <p className="text-bad text-sm sm:col-span-2">{lift.error.message}</p>}
    </div>
  )
}

function BlockList({ label, empty, rows, busy, canLift, onLift }) {
  return (
    <div className="bg-surface-2 rounded-lg p-3">
      <div className="label">
        {label} <span className="tabular-nums">· {rows.length}</span>
      </div>
      {rows.length === 0 ? (
        <p className="text-ink-3 mt-1 text-sm">{empty}</p>
      ) : (
        <ul className="mt-2 grid gap-2">
          {rows.map((row) => (
            <li key={row.id} className="flex items-center justify-between gap-2 text-sm">
              <div className="min-w-0">
                <div className="truncate font-semibold">
                  {row.nickname}
                  {row.disabled && <span className="text-bad ms-2 text-xs font-normal">switched off</span>}
                </div>
                <div className="text-ink-3 truncate text-xs">
                  <span className="font-mono">@{row.handle}</span> · {when(row.blocked_at)}
                </div>
              </div>
              <button
                type="button"
                className="btn btn-secondary h-8 shrink-0 px-3 text-xs"
                disabled={busy || !canLift}
                title={canLift ? undefined : 'Only the admin role can lift a block.'}
                onClick={() => onLift(row)}
              >
                Unblock
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function Stat({ label, value }) {
  return (
    <div className="bg-surface-2 rounded-lg p-3">
      <div className="label">{label}</div>
      <div className="mt-1 text-xl font-light tabular-nums">{value}</div>
    </div>
  )
}
