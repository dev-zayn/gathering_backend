import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, DoorClosed, X } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Fact, Note, Tag, Tile } from '../components/game-bits.jsx'
import { Code, StatusBadge } from '../components/room-bits.jsx'
import { roomFacts, when } from '../lib/games.js'
import { useMe } from '../lib/me.js'
import { endReasonLabel, eventLabel, roleLabel, roundLabel } from '../lib/rooms.js'

// One room: who is in it and who is connected, the shape its game gave it,
// the latest round's results and what happened in it, newest first. An open
// room can be closed from here, for everybody in it, after a confirmation.
export default function Room() {
  const { id } = useParams()
  const me = useMe()
  const queries = useQueryClient()
  const room = useQuery({
    queryKey: ['room', id],
    queryFn: () => api.get(`/v1/admin/rooms/${id}`),
    refetchInterval: (q) => (q.state.data?.status === 'ended' ? false : 5_000),
  })
  const [confirming, setConfirming] = useState(false)
  const [note, setNote] = useState(null)
  const close = useMutation({
    mutationFn: () => api.post(`/v1/admin/rooms/${id}/close`),
    onSuccess: (closed) => {
      setConfirming(false)
      queries.setQueryData(['room', id], closed)
      queries.invalidateQueries({ queryKey: ['rooms'] })
      setNote({ tone: 'good', text: `Closed. Every player in ${closed.code} was sent out of the room.` })
    },
    onError: (e) => {
      setConfirming(false)
      setNote({ tone: 'bad', text: e.message })
      queries.invalidateQueries({ queryKey: ['room', id] })
    },
  })

  if (room.isLoading) return <p className="text-ink-3 text-sm">Loading…</p>
  if (room.isError)
    return (
      <div>
        <p className="text-bad text-sm">
          {room.error.status === 404 ? 'No such room. A room is kept for a day after it ends; its results stay.' : room.error.message}
        </p>
        <Link to="/rooms" className="btn btn-secondary mt-4">
          <ArrowLeft className="size-4" /> Rooms
        </Link>
      </div>
    )
  const r = room.data
  const open = r.status !== 'ended'
  const canClose = me?.roles?.includes('admin')
  const names = Object.fromEntries(r.members.map((m) => [m.id, m.nickname]))
  names[r.host.id] = r.host.nickname

  return (
    <div className="grid gap-5">
      <div>
        <Link to="/rooms" className="text-ink-2 hover:text-ink inline-flex items-center gap-1 text-xs font-semibold">
          <ArrowLeft className="size-3.5" /> Rooms
        </Link>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-4">
            <Tile src={r.game.icon} name={r.game.name} size="size-14" />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-3">
                <Code big>{r.code}</Code>
                <StatusBadge status={r.status} />
                {r.visibility === 'friends' && <Tag>Friends only</Tag>}
              </div>
              <div className="text-ink-2 mt-1 text-sm">
                <Link to={`/games/${r.game.id}`} className="hover:underline">
                  {r.game.name || r.game.id}
                </Link>{' '}
                · {roundLabel(r)} · hosted by <b className="text-ink">{r.host.nickname}</b>
              </div>
            </div>
          </div>
          {open && (
            <button
              type="button"
              className="btn btn-secondary text-bad"
              disabled={!canClose || close.isPending}
              title={canClose ? undefined : 'Only an admin can close a room'}
              onClick={() => setConfirming(true)}
            >
              <DoorClosed className="size-4" /> Close the room
            </button>
          )}
        </div>
      </div>

      {note && (
        <Note tone={note.tone}>
          <span className="flex items-center justify-between gap-3">
            {note.text}
            <button type="button" aria-label="Dismiss" className="text-ink-3 hover:text-ink" onClick={() => setNote(null)}>
              <X className="size-3.5" />
            </button>
          </span>
        </Note>
      )}
      {!open && (
        <Note tone={r.end_reason === 'admin' ? 'wait' : 'good'}>
          Ended {when(r.ended_at)} UTC: {endReasonLabel[r.end_reason] ?? r.end_reason}. The room is removed a day after its end; its
          results are kept for good.
        </Note>
      )}

      <div className="grid items-start gap-5 lg:grid-cols-3">
        <div className="grid gap-5 lg:col-span-2">
          <Section title={open ? `Members · ${r.connected} of ${r.members.length} connected` : 'Members at the end'}>
            {r.members.length === 0 && <p className="text-ink-3 text-sm">Nobody.</p>}
            <ul className="divide-line -my-1 divide-y">
              {r.members.map((m) => (
                <li key={m.id} className="flex items-center gap-3 py-2">
                  <span
                    className={`size-2.5 shrink-0 rounded-full ${open && m.connected ? 'bg-good' : 'bg-line-2'}`}
                    title={open && m.connected ? 'Connected' : 'Not connected'}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-semibold">{m.nickname}</span>
                    <span className="text-ink-2 block font-mono text-xs" dir="ltr">
                      @{m.handle}
                    </span>
                  </span>
                  <span className="text-ink-2 hidden text-xs sm:block">joined {when(m.joined_at)}</span>
                  <Tag tone={m.role === 'host' ? 'bg-accent/15 text-accent-text' : undefined}>
                    {roleLabel[m.role] ?? m.role} · seat {m.seat}
                  </Tag>
                </li>
              ))}
            </ul>
          </Section>
          <Section title="What happened, newest first">
            {r.events.length === 0 && <p className="text-ink-3 text-sm">Nothing yet.</p>}
            <ul className="grid gap-1.5">
              {r.events.map((e, i) => (
                <li key={`${e.at}-${i}`} className="grid grid-cols-[auto_1fr] gap-x-3 text-sm sm:grid-cols-[9rem_1fr]">
                  <span className="text-ink-2 text-xs whitespace-nowrap tabular-nums">{when(e.at)}</span>
                  <span className="min-w-0">
                    <b>{eventLabel[e.event] ?? e.event}</b>
                    {e.player && <span> · {names[e.player] ?? <span className="font-mono text-xs">{e.player}</span>}</span>}
                    <EventDetail event={e} />
                  </span>
                </li>
              ))}
            </ul>
          </Section>
        </div>
        <div className="grid gap-5">
          <Section title={r.round ? `Round ${r.round}'s results` : 'Results'}>
            {r.results.length === 0 ? (
              <p className="text-ink-3 text-sm">{r.status === 'playing' ? 'The round is still being played.' : 'None yet.'}</p>
            ) : (
              <ol className="grid gap-2">
                {r.results.map((x) => (
                  <li key={x.player} className="flex items-center gap-3 text-sm">
                    <span className="bg-surface-2 inline-flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-bold tabular-nums">
                      {x.rank}
                    </span>
                    <span className="min-w-0 flex-1 truncate font-semibold">{x.nickname || x.player}</span>
                    <span className="tabular-nums">{x.score}</span>
                  </li>
                ))}
                <li className="text-ink-3 text-xs">Written by {r.results[0].submitted_by === 'game' ? "the game's own server" : 'the host'}.</li>
              </ol>
            )}
          </Section>
          <Section title="The room">
            {roomFacts(r.config).map(([label, value]) => (
              <Fact key={label} label={label}>
                {value}
              </Fact>
            ))}
            <div className="grid grid-cols-2 gap-3">
              <Fact label="Made">{when(r.created_at)}</Fact>
              <Fact label="Round started">{r.started_at ? when(r.started_at) : null}</Fact>
            </div>
            <Fact label="ID" mono>
              {r.id}
            </Fact>
          </Section>
        </div>
      </div>

      <Dialog
        open={confirming}
        onClose={() => !close.isPending && setConfirming(false)}
        title={`Close ${r.code}?`}
        description={`Everybody in it is sent out of the room, back to the app's home, and told the room was closed. ${
          r.status === 'playing'
            ? "The round being played ends now: its results are the game's if its server already wrote them, otherwise it has none."
            : 'Nothing is being played; the last round’s results are kept.'
        } A closed room cannot be opened again.`}
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setConfirming(false)} disabled={close.isPending}>
              Cancel
            </button>
            <button type="button" className="btn btn-primary" onClick={() => close.mutate()} disabled={close.isPending}>
              {close.isPending ? 'Closing…' : `Yes, send ${r.members.length === 1 ? 'the player' : `all ${r.members.length}`} out`}
            </button>
          </>
        }
      >
        <p className="text-sm">
          {r.connected > 0
            ? `${r.connected} ${r.connected === 1 ? 'player is' : 'players are'} connected right now.`
            : 'Nobody is connected right now.'}
        </p>
      </Dialog>
    </div>
  )
}

function Section({ title, children, className = '' }) {
  return (
    <section className={`card p-4 ${className}`}>
      <h2 className="text-sm font-extrabold">{title}</h2>
      <div className="mt-3 grid gap-3">{children}</div>
    </section>
  )
}

// The few details worth a word; the rest are the API's own record.
function EventDetail({ event: e }) {
  const d = e.detail ?? {}
  let text = null
  if (e.event === 'ended') text = endReasonLabel[d.reason] ?? d.reason
  else if (e.event === 'finished') text = `round ${d.round}${d.reason === 'timeout' ? ', past its time limit' : ''}`
  else if (e.event === 'started') text = `round ${d.round}, ${d.members} in the room`
  else if (e.event === 'results')
    text = `round ${d.round}: ${d.scores} ${d.scores === 1 ? 'score' : 'scores'} from ${d.by === 'game' ? "the game's server" : 'the host'}`
  else if (e.event === 'joined' && d.rejoin) text = 'came back'
  else if (e.event === 'created') text = `code ${d.code}`
  if (!text) return null
  return <span className="text-ink-2"> · {text}</span>
}
