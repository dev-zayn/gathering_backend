import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, Ban, Flag, Gavel, Play, X } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Fact, Note, Tag, Tile } from '../components/game-bits.jsx'
import { Code } from '../components/room-bits.jsx'
import { TournamentStatus } from '../components/tournament-bits.jsx'
import { when } from '../lib/games.js'
import { useMe } from '../lib/me.js'
import {
  decidedLabel,
  endReasonLabel,
  entrantStatusLabel,
  formatLabel,
  lengthLabel,
  matchStatusLabel,
  stageLabel,
  visibilityLabel,
} from '../lib/tournaments.js'

// One tournament: its entrants, its bracket a stage at a time or its
// league's table, and the console's hand in it — start it, call it off,
// finish a league now, decide a ready match — each behind a confirmation
// that says what follows. A player's tournament too: an admin is the
// organizer of every one.
export default function Tournament() {
  const { id } = useParams()
  const me = useMe()
  const queries = useQueryClient()
  const tournament = useQuery({
    queryKey: ['tournament', id],
    queryFn: () => api.get(`/v1/admin/tournaments/${id}`),
    refetchInterval: (q) => (['done', 'cancelled'].includes(q.state.data?.status) ? false : 8_000),
  })
  const [asking, setAsking] = useState(null)
  const [note, setNote] = useState(null)
  const [stage, setStage] = useState(null)

  const act = useMutation({
    mutationFn: ({ path, body }) => api.post(`/v1/admin/tournaments/${id}${path}`, body),
    onSuccess: (next, { done }) => {
      setAsking(null)
      queries.setQueryData(['tournament', id], next)
      queries.invalidateQueries({ queryKey: ['tournaments'] })
      setNote({ tone: 'good', text: done })
    },
    onError: (e) => {
      setAsking(null)
      setNote({ tone: 'bad', text: e.message })
      queries.invalidateQueries({ queryKey: ['tournament', id] })
    },
  })

  if (tournament.isLoading) return <p className="text-ink-3 text-sm">Loading…</p>
  if (tournament.isError)
    return (
      <div>
        <p className="text-bad text-sm">{tournament.error.status === 404 ? 'No such tournament.' : tournament.error.message}</p>
        <Link to="/tournaments" className="btn btn-secondary mt-4">
          <ArrowLeft className="size-4" /> Tournaments
        </Link>
      </div>
    )
  const t = tournament.data
  const canAct = me?.roles?.includes('admin')
  const live = t.status === 'open' || t.status === 'running'
  const shownStage = stage ?? t.stage ?? t.stages
  const stageMatches = t.matches.filter((m) => m.stage === shownStage)

  const confirmations = {
    start: {
      title: `Start ${t.name}?`,
      description: `The draw seeds the ${t.entrants.length} entrants at random${
        t.format === 'knockout' ? ', writes the bracket (the top seeds get the byes) and opens the first matches' : ' and opens the league'
      }. Every entrant is told. Nobody can join after it starts.`,
      cta: 'Start it',
      run: () => act.mutate({ path: '/start', done: 'Started. The entrants were told.' }),
    },
    cancel: {
      title: `Call ${t.name} off?`,
      description:
        'For good: nobody wins, every match room being played closes, and every entrant is told it will not go on. It cannot be started again.',
      cta: 'Call it off',
      run: () => act.mutate({ path: '/cancel', done: 'Called off. The entrants were told.' }),
    },
    finish: {
      title: `Finish ${t.name} now?`,
      description:
        'The table as it stands decides the places, and the first is the champion. A round being played closes without being counted.',
      cta: 'Finish it',
      run: () => act.mutate({ path: '/finish', done: 'Finished. The champion and the places are set.' }),
    },
  }
  const award = asking?.award
  const asked = asking && !award ? confirmations[asking] : null

  return (
    <div className="grid gap-5">
      <div>
        <Link to="/tournaments" className="text-ink-2 hover:text-ink inline-flex items-center gap-1 text-xs font-semibold">
          <ArrowLeft className="size-3.5" /> Tournaments
        </Link>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-4">
            <Tile src={t.game.icon} name={t.game.name} size="size-14" />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-3">
                <h1 className="text-xl font-extrabold" dir="auto">
                  {t.name}
                </h1>
                <TournamentStatus status={t.status} />
                {t.official && <Tag tone="bg-accent/20 text-accent-ink">Official</Tag>}
              </div>
              <div className="text-ink-2 mt-1 flex flex-wrap items-center gap-2 text-sm">
                <Code>{t.code}</Code> ·{' '}
                <Link to={`/games/${t.game.id}`} className="hover:underline">
                  {t.game.name || t.game.id}
                </Link>{' '}
                · {formatLabel[t.format]} · {lengthLabel(t)}
                {t.organizer && (
                  <span>
                    · made by <b className="text-ink">{t.organizer.nickname}</b>
                  </span>
                )}
              </div>
            </div>
          </div>
          {live && (
            <div className="flex flex-wrap gap-2">
              {t.status === 'open' && (
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={!canAct || act.isPending || t.entrants.length < t.min}
                  title={!canAct ? 'Only an admin can start a tournament' : t.entrants.length < t.min ? `It needs ${t.min} entrants` : undefined}
                  onClick={() => setAsking('start')}
                >
                  <Play className="size-4" /> Start the draw
                </button>
              )}
              {t.status === 'running' && t.format === 'league' && (
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={!canAct || act.isPending || (t.matches[0]?.played ?? 0) === 0}
                  title={(t.matches[0]?.played ?? 0) === 0 ? 'No round has been counted yet' : undefined}
                  onClick={() => setAsking('finish')}
                >
                  <Flag className="size-4" /> Finish now
                </button>
              )}
              <button
                type="button"
                className="btn btn-secondary text-bad"
                disabled={!canAct || act.isPending}
                title={canAct ? undefined : 'Only an admin can call a tournament off'}
                onClick={() => setAsking('cancel')}
              >
                <Ban className="size-4" /> Call off
              </button>
            </div>
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
      {!live && (
        <Note tone={t.status === 'done' ? 'good' : 'wait'}>
          {t.status === 'done' ? 'Done' : 'Called off'} {when(t.ended_at)} UTC: {endReasonLabel[t.end_reason] ?? t.end_reason}.
          {t.champion && (
            <>
              {' '}
              Champion: <b>{t.champion.nickname}</b>.
            </>
          )}
        </Note>
      )}
      {t.status === 'open' && t.starts_at && <Note tone="wait">The clock starts it at {when(t.starts_at)} UTC, or calls it off if fewer than {t.min} have joined.</Note>}

      <div className="grid items-start gap-5 lg:grid-cols-3">
        <div className="grid gap-5 lg:col-span-2">
          {t.format === 'knockout' && t.matches.length > 0 && (
            <Section title="The bracket">
              <div className="flex flex-wrap gap-2">
                {Array.from({ length: t.stages }, (_, i) => i + 1).map((s) => (
                  <button
                    key={s}
                    type="button"
                    className={`btn h-8 px-3 text-xs ${s === shownStage ? 'btn-primary' : 'btn-secondary'}`}
                    onClick={() => setStage(s)}
                  >
                    {stageLabel(s, t.stages)}
                  </button>
                ))}
              </div>
              <ul className="divide-line -my-1 divide-y">
                {stageMatches.map((m) => (
                  <li key={m.id} className="grid gap-2 py-2 sm:grid-cols-[1fr_auto] sm:items-center">
                    <div className="grid gap-1">
                      {m.sides.map((side, i) => (
                        <div key={i} className="flex items-center gap-2 text-sm">
                          <span className={`min-w-0 flex-1 truncate ${m.winner && side.player?.id === m.winner ? 'font-extrabold' : ''}`}>
                            {side.player ? side.player.nickname : side.void ? <span className="text-ink-3">Nobody (a bye)</span> : <span className="text-ink-3">To be decided</span>}
                            {side.withdrawn && <span className="text-ink-3"> · withdrew</span>}
                          </span>
                          <span className="tabular-nums">{side.player ? side.wins : ''}</span>
                        </div>
                      ))}
                    </div>
                    <div className="flex flex-wrap items-center gap-2 sm:justify-end">
                      <Tag tone={m.status === 'ready' ? 'bg-good/10 text-good' : undefined}>
                        {m.status === 'done' ? decidedLabel[m.decided_by] ?? 'Decided' : matchStatusLabel[m.status]}
                      </Tag>
                      {m.room && (
                        <Link to={`/rooms/${m.room.id}`} className="text-xs hover:underline">
                          room <Code>{m.room.code}</Code>
                        </Link>
                      )}
                      {t.status === 'running' && m.status === 'ready' && (
                        <button
                          type="button"
                          className="btn btn-secondary h-8 px-3 text-xs"
                          disabled={!canAct || act.isPending}
                          onClick={() => setAsking({ award: m })}
                        >
                          <Gavel className="size-3.5" /> Decide
                        </button>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            </Section>
          )}
          {t.format === 'league' && t.status !== 'open' && (
            <Section title={`The table · ${t.matches[0]?.played ?? 0} of ${t.rounds} rounds counted`}>
              <ol className="grid gap-2">
                {t.standings.map((s) => (
                  <li key={s.player.id} className="flex items-center gap-3 text-sm">
                    <span className="bg-surface-2 inline-flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-bold tabular-nums">
                      {s.rank}
                    </span>
                    <span className="min-w-0 flex-1 truncate font-semibold">
                      {s.player.nickname}
                      {s.withdrawn && <span className="text-ink-3 font-normal"> · withdrew</span>}
                    </span>
                    <span className="text-ink-2 text-xs tabular-nums">
                      {s.wins} first · {s.played} played
                    </span>
                    <span className="w-14 text-end font-bold tabular-nums">{s.points} pts</span>
                  </li>
                ))}
              </ol>
              {t.matches[0]?.room && (
                <p className="text-xs">
                  Being played in room{' '}
                  <Link to={`/rooms/${t.matches[0].room.id}`} className="hover:underline">
                    <Code>{t.matches[0].room.code}</Code>
                  </Link>
                  .
                </p>
              )}
            </Section>
          )}
          <Section title={`Entrants · ${t.entrants.length} of ${t.size}`}>
            {t.entrants.length === 0 && <p className="text-ink-3 text-sm">Nobody yet.</p>}
            <ul className="divide-line -my-1 divide-y">
              {t.entrants.map((e) => (
                <li key={e.id} className="flex items-center gap-3 py-2">
                  <span className="text-ink-2 w-8 text-xs tabular-nums">#{e.seed}</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-semibold">{e.nickname}</span>
                    <span className="text-ink-2 block font-mono text-xs" dir="ltr">
                      @{e.handle}
                    </span>
                  </span>
                  {e.place && <Tag tone={e.place === 1 ? 'bg-accent/20 text-accent-ink' : undefined}>Place {e.place}</Tag>}
                  <Tag tone={e.status === 'in' ? 'bg-good/10 text-good' : undefined}>{entrantStatusLabel[e.status] ?? e.status}</Tag>
                </li>
              ))}
            </ul>
          </Section>
        </div>
        <div className="grid gap-5">
          <Section title="The tournament">
            <div className="grid grid-cols-2 gap-3">
              <Fact label="Format">{formatLabel[t.format]}</Fact>
              <Fact label="Matches">{lengthLabel(t)}</Fact>
              <Fact label="Takes">{`${t.min} to ${t.size}`}</Fact>
              <Fact label="Who joins">{visibilityLabel[t.visibility] ?? t.visibility}</Fact>
              <Fact label="Made">{when(t.created_at)}</Fact>
              <Fact label="Started">{t.started_at ? when(t.started_at) : null}</Fact>
            </div>
            <Fact label="Link" mono>
              {t.link}
            </Fact>
            <Fact label="ID" mono>
              {t.id}
            </Fact>
          </Section>
          <Section title="Its matches">
            <p className="text-ink-2 text-xs">
              Each match is played in a room of the game, made by the first of its players to press play: a knockout match seats its
              two, a league seats every entrant still in it. Every round that ends with results counts; the round that decides a match
              closes its room.
            </p>
          </Section>
        </div>
      </div>

      <Dialog
        open={Boolean(asked)}
        onClose={() => !act.isPending && setAsking(null)}
        title={asked?.title}
        description={asked?.description}
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setAsking(null)} disabled={act.isPending}>
              Cancel
            </button>
            <button type="button" className="btn btn-primary" onClick={() => asked?.run()} disabled={act.isPending}>
              {act.isPending ? 'Working…' : asked?.cta}
            </button>
          </>
        }
      />
      <Dialog
        open={Boolean(award)}
        onClose={() => !act.isPending && setAsking(null)}
        title={award ? `Decide this ${stageLabel(award.stage, t.stages).toLowerCase()} match?` : ''}
        description="The player you pick goes on and the other is out, as if they had won it. Its room, if one is open, closes."
        footer={
          <button type="button" className="btn btn-secondary" onClick={() => setAsking(null)} disabled={act.isPending}>
            Cancel
          </button>
        }
      >
        <div className="grid gap-2">
          {award?.sides.map((side) =>
            side.player ? (
              <button
                key={side.player.id}
                type="button"
                className="btn btn-primary justify-between"
                disabled={act.isPending}
                onClick={() =>
                  act.mutate({
                    path: `/matches/${award.id}/award`,
                    body: { player: side.player.id },
                    done: `Decided for ${side.player.nickname}.`,
                  })
                }
              >
                <span>{side.player.nickname} wins</span>
                <span className="tabular-nums">{side.wins} so far</span>
              </button>
            ) : null,
          )}
        </div>
      </Dialog>
    </div>
  )
}

function Section({ title, children }) {
  return (
    <section className="card p-4">
      <h2 className="text-sm font-extrabold">{title}</h2>
      <div className="mt-3 grid gap-3">{children}</div>
    </section>
  )
}
