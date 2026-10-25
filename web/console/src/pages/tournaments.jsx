import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ChevronRight, Plus } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Tag, Tile } from '../components/game-bits.jsx'
import { Pager } from '../components/list-bits.jsx'
import { Code } from '../components/room-bits.jsx'
import { TournamentStatus } from '../components/tournament-bits.jsx'
import { when } from '../lib/games.js'
import { useMe } from '../lib/me.js'
import { formatLabel, lengthLabel, stageLabel, STATUSES, statusFilterLabel } from '../lib/tournaments.js'

// Every tournament, a player's and the official ones, newest first: the live
// ones by default, any status or one game on asking. An admin makes an
// official one here: public, listed for every player, started by an admin or
// by the clock at its start time.
export default function Tournaments() {
  const me = useMe()
  const [params, setParams] = useSearchParams()
  const asked = params.get('status')
  const status = STATUSES.includes(asked) ? asked : 'live'
  const game = params.get('game') ?? ''
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]
  const [making, setMaking] = useState(false)

  const list = useQuery({
    queryKey: ['tournaments', status, game, cursor],
    queryFn: () =>
      api.get(
        `/v1/admin/tournaments?status=${status}&game=${encodeURIComponent(game)}&limit=50&cursor=${encodeURIComponent(cursor)}`,
      ),
    refetchInterval: 15_000,
  })
  const games = useQuery({ queryKey: ['games'], queryFn: () => api.get('/v1/admin/games') })

  function filter(next) {
    const merged = { status, game, ...next }
    const out = {}
    if (merged.status !== 'live') out.status = merged.status
    if (merged.game) out.game = merged.game
    setParams(out)
    setCursors([''])
  }

  const canMake = me?.roles?.includes('admin')
  const rows = list.data?.items ?? []
  return (
    <div>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-extrabold">Tournaments</h1>
          <p className="text-ink-2 mt-1 text-sm">Knockouts and leagues: the players' own, and the official ones made here.</p>
        </div>
        <button
          type="button"
          className="btn btn-primary"
          disabled={!canMake}
          title={canMake ? undefined : 'Only an admin can make a tournament'}
          onClick={() => setMaking(true)}
        >
          <Plus className="size-4" /> New official tournament
        </button>
      </div>

      <div className="mt-5 flex flex-wrap items-center gap-2">
        {STATUSES.map((s) => (
          <button
            key={s}
            type="button"
            className="chip"
            aria-pressed={s === status}
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
        <div className="text-ink-3 border-line bg-surface-2/60 hidden grid-cols-[2fr_1fr_1.2fr_1.2fr_0.8fr_1fr_16px] gap-3 border-b px-4 py-2 text-xs font-semibold md:grid">
          <span>Tournament</span>
          <span>Status</span>
          <span>Format</span>
          <span>Organizer</span>
          <span className="text-end">Entrants</span>
          <span>Made</span>
          <span />
        </div>
        {list.isLoading && <p className="text-ink-3 px-4 py-6 text-sm">Loading…</p>}
        {list.isError && <p className="text-bad px-4 py-6 text-sm">{list.error.message}</p>}
        {list.data && rows.length === 0 && (
          <p className="text-ink-3 px-4 py-6 text-sm">
            {status === 'live' && !game ? 'No tournament is taking entrants or being played.' : 'No tournament matches.'}
          </p>
        )}
        {rows.map((t) => (
          <Link
            key={t.id}
            to={`/tournaments/${t.id}`}
            className="border-line hover:bg-surface-2/60 grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 border-b px-4 py-3 text-sm last:border-0 md:grid-cols-[2fr_1fr_1.2fr_1.2fr_0.8fr_1fr_16px] md:py-2"
          >
            <span className="flex min-w-0 items-center gap-3">
              <Tile src={t.game.icon} name={t.game.name} size="size-8" />
              <span className="min-w-0">
                <span className="block truncate font-semibold" dir="auto">
                  {t.name}
                </span>
                <span className="text-ink-2 flex items-center gap-2 text-xs">
                  <Code>{t.code}</Code> · {t.game.name || t.game.id}
                </span>
              </span>
            </span>
            <span className="justify-self-end md:justify-self-start">
              <TournamentStatus status={t.status} />
            </span>
            <span className="text-ink-2 text-xs">
              {formatLabel[t.format] ?? t.format} · {lengthLabel(t)}
              {t.status === 'running' && t.format === 'knockout' && t.stage && (
                <span className="block">{stageLabel(t.stage, t.stages)}</span>
              )}
              {t.champion && <span className="block">Won by {t.champion.nickname}</span>}
            </span>
            <span className="min-w-0 truncate text-xs">
              {t.official ? (
                <Tag tone="bg-accent/15 text-accent-text">Official</Tag>
              ) : (
                <>
                  <span className="font-semibold">{t.organizer?.nickname}</span>{' '}
                  <span className="text-ink-2 font-mono" dir="ltr">
                    @{t.organizer?.handle}
                  </span>
                </>
              )}
            </span>
            <span className="text-xs tabular-nums md:text-end">
              <b>{t.entrants}</b> <span className="text-ink-2">of {t.size}</span>
            </span>
            <span className="text-ink-2 text-xs whitespace-nowrap">
              {when(t.created_at)}
              {t.status === 'open' && t.starts_at && <span className="block">starts {when(t.starts_at)}</span>}
            </span>
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

      <NewTournament open={making} onClose={() => setMaking(false)} games={games.data?.games ?? []} />
    </div>
  )
}

// The form for an official tournament: the game (published, seating two at
// a table), its name, its format and length, how many it takes, and when the
// clock starts it. Every refusal comes back in words.
function NewTournament({ open, onClose, games }) {
  const navigate = useNavigate()
  const queries = useQueryClient()
  const [gameId, setGameId] = useState('')
  const [name, setName] = useState('')
  const [format, setFormat] = useState('knockout')
  const [bestOf, setBestOf] = useState(1)
  const [rounds, setRounds] = useState(5)
  const [size, setSize] = useState(16)
  const [startsAt, setStartsAt] = useState('')

  const playable = games.filter((g) => g.listing === 'published' && (g.room?.max ?? 0) >= 2)
  const picked = playable.find((g) => g.id === gameId)
  const duels = !picked || (picked.room?.min ?? 1) <= 2
  const tableMax = picked ? Math.min(picked.room?.max ?? 2, 64) : 64
  const tableMin = picked ? Math.max(2, picked.room?.min ?? 2) : 2

  const make = useMutation({
    mutationFn: () =>
      api.post('/v1/admin/tournaments', {
        game: gameId,
        name: name.trim(),
        format,
        ...(format === 'knockout' ? { best_of: Number(bestOf) } : { rounds: Number(rounds) }),
        size: Number(size),
        ...(startsAt ? { starts_at: new Date(startsAt).toISOString() } : {}),
      }),
    onSuccess: (t) => {
      queries.invalidateQueries({ queryKey: ['tournaments'] })
      onClose()
      navigate(`/tournaments/${t.id}`)
    },
  })

  function pickGame(id) {
    setGameId(id)
    const g = playable.find((x) => x.id === id)
    if (!g) return
    if (!name.trim()) setName(`بطولة ${g.name}`)
    if ((g.room?.min ?? 1) > 2) setFormat('league')
    if (format === 'league' || (g.room?.min ?? 1) > 2) setSize(Math.min(g.room?.max ?? 2, 64))
  }

  const ready = gameId && name.trim() && Number(size) >= 2
  return (
    <Dialog
      open={open}
      onClose={() => !make.isPending && onClose()}
      title="New official tournament"
      description="Public: every player sees it under the official tournaments and joins it. It has no organizer; an admin starts it here, or the clock at its start time (calling it off if too few have joined)."
      footer={
        <>
          <button type="button" className="btn btn-secondary" onClick={onClose} disabled={make.isPending}>
            Cancel
          </button>
          <button type="button" className="btn btn-primary" disabled={!ready || make.isPending} onClick={() => make.mutate()}>
            {make.isPending ? 'Making…' : 'Make it'}
          </button>
        </>
      }
    >
      <div className="grid gap-3">
        <label className="grid gap-1 text-sm font-semibold">
          Game
          <select className="field font-normal" value={gameId} onChange={(e) => pickGame(e.target.value)}>
            <option value="">Pick a published game…</option>
            {playable.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name} · {g.room.min === g.room.max ? g.room.min : `${g.room.min}–${g.room.max}`} players
              </option>
            ))}
          </select>
        </label>
        <label className="grid gap-1 text-sm font-semibold">
          Name
          <input className="field font-normal" dir="auto" maxLength={40} value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <div className="grid gap-1 text-sm font-semibold">
          Format
          <div className="flex gap-2">
            {['knockout', 'league'].map((f) => (
              <button
                key={f}
                type="button"
                className="chip"
                aria-pressed={format === f}
                disabled={f === 'knockout' && !duels}
                title={f === 'knockout' && !duels ? 'This game needs more than two at a table' : undefined}
                onClick={() => {
                  setFormat(f)
                  setSize(f === 'league' ? tableMax : 16)
                }}
              >
                {formatLabel[f]}
              </button>
            ))}
          </div>
          <span className="text-ink-3 text-xs font-normal">
            {format === 'knockout'
              ? 'Duels in a bracket: the winner of each match goes on, the loser is out.'
              : 'Everybody at one table for a set number of rounds; a point for every player beaten in a round.'}
          </span>
        </div>
        <div className="grid grid-cols-2 gap-3">
          {format === 'knockout' ? (
            <label className="grid gap-1 text-sm font-semibold">
              A match is
              <select className="field font-normal" value={bestOf} onChange={(e) => setBestOf(e.target.value)}>
                <option value={1}>One round</option>
                <option value={3}>Best of 3</option>
                <option value={5}>Best of 5</option>
              </select>
            </label>
          ) : (
            <label className="grid gap-1 text-sm font-semibold">
              Rounds
              <input className="field font-normal" type="number" min={1} max={20} value={rounds} onChange={(e) => setRounds(e.target.value)} />
            </label>
          )}
          <label className="grid gap-1 text-sm font-semibold">
            Most entrants
            <input
              className="field font-normal"
              type="number"
              min={format === 'league' ? tableMin : 2}
              max={format === 'league' ? tableMax : 64}
              value={size}
              onChange={(e) => setSize(e.target.value)}
            />
          </label>
        </div>
        <label className="grid gap-1 text-sm font-semibold">
          Starts at (optional, your time)
          <input className="field font-normal" type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} />
        </label>
        {make.isError && <p className="text-bad text-sm">{make.error.message}</p>}
      </div>
    </Dialog>
  )
}
