import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Megaphone, Send, X } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Note, Tag } from '../components/game-bits.jsx'
import { Pager } from '../components/list-bits.jsx'
import { when } from '../lib/games.js'
import { useMe } from '../lib/me.js'

// The limits the API holds an announcement to, counted as it counts them.
const TITLE_MAX = 60
const BODY_MAX = 240
const PLAYERS_MAX = 500

const kinds = [
  ['all', 'Everyone'],
  ['game', "A game's players"],
  ['players', 'Chosen players'],
]

// @handles or ids, however they were pasted: spaces, commas, new lines.
const refsOf = (text) => text.split(/[\s,،]+/).filter(Boolean)

const length = (s) => [...s.trim()].length

// Announcements: one line in the inbox of every player the audience
// reaches. The form, the line as the app draws it, a confirmation that names
// how many it reaches (a dry run) before anything is sent, and under it what
// was sent before.
export default function Notifications() {
  const me = useMe()
  const queries = useQueryClient()
  const games = useQuery({ queryKey: ['games'], queryFn: () => api.get('/v1/admin/games') })
  const [kind, setKind] = useState('all')
  const [audienceGame, setAudienceGame] = useState('')
  const [players, setPlayers] = useState('')
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [opens, setOpens] = useState('')
  const [reach, setReach] = useState(null)
  const [note, setNote] = useState(null)

  const canSend = me?.roles?.includes('admin')
  const refs = refsOf(players)
  const gameList = games.data?.games ?? []
  const opened = gameList.find((g) => g.id === opens)

  function payload(dryRun) {
    const audience = { kind }
    if (kind === 'game') audience.game = audienceGame
    if (kind === 'players') audience.players = refs
    const out = { audience, title: title.trim(), body: body.trim(), dry_run: dryRun }
    if (opens) out.game = opens
    return out
  }

  const count = useMutation({
    mutationFn: () => api.post('/v1/admin/notifications', payload(true)),
    onSuccess: (r) => setReach(r.reach),
  })
  const send = useMutation({
    mutationFn: () => api.post('/v1/admin/notifications', payload(false)),
    onSuccess: (r) => {
      setReach(null)
      setNote({ tone: 'good', text: `Sent. It is in the inbox of ${r.reach} ${r.reach === 1 ? 'player' : 'players'}.` })
      setTitle('')
      setBody('')
      setPlayers('')
      setOpens('')
      queries.invalidateQueries({ queryKey: ['announcements'] })
    },
    onError: () => setReach(null),
  })
  const error = (count.error ?? send.error)?.message

  // What the form can tell before asking: the rest the API says in words.
  const problems = []
  if (length(title) > TITLE_MAX) problems.push(`The title is ${length(title)} characters; at most ${TITLE_MAX}.`)
  if (length(body) > BODY_MAX) problems.push(`The body is ${length(body)} characters; at most ${BODY_MAX}.`)
  if (kind === 'players' && refs.length > PLAYERS_MAX) problems.push(`${refs.length} players named; at most ${PLAYERS_MAX}.`)
  const ready = canSend && length(title) > 0 && problems.length === 0 && (kind !== 'game' || audienceGame) && (kind !== 'players' || refs.length > 0)

  return (
    <div className="grid gap-5">
      <div>
        <h1 className="text-xl font-extrabold">Notifications</h1>
        <p className="text-ink-2 mt-1 text-sm">
          An announcement puts one line in the inbox of every player it reaches. Nothing is pushed to phones yet.
        </p>
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
      {!canSend && <Note tone="wait">Only an admin can send an announcement. Your account can read what was sent.</Note>}

      <div className="grid gap-5 lg:grid-cols-[3fr_2fr]">
        <form
          className="card grid gap-4 p-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (!ready) return
            setNote(null)
            send.reset()
            count.mutate()
          }}
        >
          <fieldset className="grid gap-2">
            <legend className="text-sm font-semibold">Who hears it</legend>
            <div className="flex flex-wrap gap-1">
              {kinds.map(([k, label]) => (
                <button
                  key={k}
                  type="button"
                  className={`btn h-8 px-3 text-xs ${kind === k ? 'btn-primary' : 'btn-secondary'}`}
                  aria-pressed={kind === k}
                  onClick={() => setKind(k)}
                >
                  {label}
                </button>
              ))}
            </div>
            {kind === 'all' && <p className="text-ink-2 text-xs">Every player who is not switched off.</p>}
            {kind === 'game' && (
              <label className="grid gap-1 text-xs">
                <span className="text-ink-2">Everybody, not switched off, who has opened this game:</span>
                <select className="field text-sm" value={audienceGame} onChange={(e) => setAudienceGame(e.target.value)}>
                  <option value="">Pick a game…</option>
                  {gameList.map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name} · {g.id}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {kind === 'players' && (
              <label className="grid gap-1 text-xs">
                <span className="text-ink-2">
                  @handles or player ids, separated by spaces, commas or lines. Up to {PLAYERS_MAX}; unknown and switched-off
                  players are skipped.
                </span>
                <textarea
                  className="field h-auto py-2 font-mono text-xs"
                  rows={3}
                  dir="ltr"
                  spellCheck={false}
                  placeholder="@zayn_4821 @sara_1203"
                  value={players}
                  onChange={(e) => setPlayers(e.target.value)}
                />
                <span className="text-ink-3">{refs.length} named</span>
              </label>
            )}
          </fieldset>

          <label className="grid gap-1 text-sm font-semibold">
            <span className="flex justify-between">
              Title <Counter n={length(title)} max={TITLE_MAX} />
            </span>
            <input className="field font-normal" dir="auto" value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <label className="grid gap-1 text-sm font-semibold">
            <span className="flex justify-between">
              Body <Counter n={length(body)} max={BODY_MAX} />
            </span>
            <textarea className="field h-auto py-2 font-normal" dir="auto" rows={3} value={body} onChange={(e) => setBody(e.target.value)} />
          </label>
          <label className="grid gap-1 text-sm font-semibold">
            A game to open (optional)
            <select className="field font-normal" value={opens} onChange={(e) => setOpens(e.target.value)}>
              <option value="">None: tapping it only marks it read</option>
              {gameList.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name} · {g.id}
                </option>
              ))}
            </select>
            <span className="text-ink-3 text-xs font-normal">Tapping the line opens the game's page in the store.</span>
          </label>

          {problems.map((p) => (
            <p key={p} className="text-bad text-sm">
              {p}
            </p>
          ))}
          {error && <p className="text-bad text-sm">{error}</p>}
          <div className="flex justify-end">
            <button type="submit" className="btn btn-primary" disabled={!ready || count.isPending || send.isPending}>
              <Send className="size-4" /> {count.isPending ? 'Counting…' : 'Review and send'}
            </button>
          </div>
        </form>

        <Preview title={title} body={body} game={opened} />
      </div>

      <Dialog
        open={reach !== null}
        onClose={() => !send.isPending && setReach(null)}
        title={reach === 0 ? 'Nobody to send it to' : `Send it to ${reach} ${reach === 1 ? 'player' : 'players'}?`}
        description={
          reach === 0
            ? 'This audience reaches nobody right now, so there is nothing to send.'
            : 'It goes into their inboxes at once and cannot be taken back.'
        }
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setReach(null)} disabled={send.isPending}>
              {reach === 0 ? 'Back' : 'Cancel'}
            </button>
            {reach > 0 && (
              <button type="button" className="btn btn-primary" onClick={() => send.mutate()} disabled={send.isPending}>
                {send.isPending ? 'Sending…' : `Yes, send to ${reach}`}
              </button>
            )}
          </>
        }
      >
        <Row title={title} body={body} />
      </Dialog>

      <Sent />
    </div>
  )
}

function Counter({ n, max }) {
  return <span className={`text-xs font-normal tabular-nums ${n > max ? 'text-bad' : 'text-ink-3'}`}>{`${n}/${max}`}</span>
}

// The line as the app draws it in the inbox: the platform's mark where a
// player's avatar or a game's icon would be, the title, the time, the unread
// dot, and the body in full. Arabic first, so right to left.
function Row({ title, body }) {
  return (
    <div dir="rtl" className="bg-surface flex gap-3 rounded-xl border border-line p-3 text-start">
      <span className="bg-accent text-accent-ink inline-flex size-10 shrink-0 items-center justify-center rounded-full text-base font-extrabold">
        ج
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span dir="auto" className="min-w-0 flex-1 font-bold [overflow-wrap:anywhere]">
            {title.trim() || 'العنوان'}
          </span>
          <span className="text-ink-3 shrink-0 text-xs">الآن</span>
          <span className="bg-accent size-2 shrink-0 rounded-full" aria-label="unread" />
        </span>
        {body.trim() && (
          <span dir="auto" className="text-ink-2 mt-0.5 block text-sm whitespace-pre-line [overflow-wrap:anywhere]">
            {body.trim()}
          </span>
        )}
      </span>
    </div>
  )
}

function Preview({ title, body, game }) {
  return (
    <section className="card grid content-start gap-3 p-4">
      <h2 className="text-sm font-extrabold">In the app's inbox</h2>
      <div className="bg-ground rounded-2xl p-3">
        <Row title={title} body={body} />
      </div>
      <p className="text-ink-2 text-xs">
        {game ? `Tapping it marks it read and opens ${game.name}'s page in the store.` : 'Tapping it marks it read.'}
      </p>
    </section>
  )
}

const audienceWords = (a, names) => {
  if (a?.kind === 'all') return 'Everyone'
  if (a?.kind === 'game') return `Players of ${names[a.game] ?? a.game}`
  if (a?.kind === 'players') return `${a.players?.length ?? 0} chosen ${a.players?.length === 1 ? 'player' : 'players'}`
  return a?.kind ?? '—'
}

// What was sent, newest first, a page at a time.
function Sent() {
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]
  const list = useQuery({
    queryKey: ['announcements', cursor],
    queryFn: () => api.get(`/v1/admin/notifications?limit=20&cursor=${encodeURIComponent(cursor)}`),
  })
  const games = useQuery({ queryKey: ['games'], queryFn: () => api.get('/v1/admin/games') })
  const names = Object.fromEntries((games.data?.games ?? []).map((g) => [g.id, g.name]))
  const items = list.data?.items ?? []

  return (
    <section>
      <h2 className="flex items-center gap-2 text-base font-extrabold">
        <Megaphone className="size-4" /> Sent
      </h2>
      <div className="card divide-line mt-3 divide-y">
        {list.isLoading && <p className="text-ink-3 p-4 text-sm">Loading…</p>}
        {list.isError && <p className="text-bad p-4 text-sm">{list.error.message}</p>}
        {list.data && items.length === 0 && <p className="text-ink-3 p-4 text-sm">Nothing has been sent yet.</p>}
        {items.map((a) => (
          <div key={a.id} className="grid gap-1 p-4 sm:grid-cols-[1fr_auto] sm:gap-4">
            <div className="min-w-0">
              {/* Each line keeps its own direction and the row's alignment. */}
              <div className="font-semibold [overflow-wrap:anywhere]">
                <span dir="auto">{a.title}</span>
              </div>
              {a.body && (
                <div className="text-ink-2 text-sm [overflow-wrap:anywhere]">
                  <span dir="auto">{a.body}</span>
                </div>
              )}
              <div className="mt-1 flex flex-wrap items-center gap-1.5">
                <Tag>{audienceWords(a.audience, names)}</Tag>
                {a.game && <Tag tone="bg-info/10 text-info">Opens {a.game.name || a.game.id}</Tag>}
              </div>
            </div>
            <div className="text-ink-2 text-xs sm:text-end">
              <div>
                <b className="text-ink tabular-nums">{a.reach}</b> reached
              </div>
              <div>
                {when(a.created_at)} · {a.admin.name || a.admin.id}
              </div>
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
    </section>
  )
}
