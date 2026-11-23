import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'

import { api } from '../api.js'
import { BuildBadge } from '../components/build-bits.jsx'
import { ListingBadge, Tile } from '../components/game-bits.jsx'
import { when } from '../lib/games.js'

const FILTERS = [
  ['in_review', 'Waiting'],
  ['approved', 'Approved'],
  ['rejected', 'Changes asked'],
  ['all', 'All'],
]

// What publishers sent to review (docs/PUBLISHERS.md): the oldest waiting
// first, so nobody waits behind a newcomer.
export default function Review() {
  const [params, setParams] = useSearchParams()
  const asked = params.get('status')
  const status = FILTERS.some(([k]) => k === asked) ? asked : 'in_review'
  const list = useQuery({ queryKey: ['review', status], queryFn: () => api.get(`/v1/admin/review?status=${status}`) })
  const rows = list.data?.items ?? []

  return (
    <div>
      <h1 className="text-2xl font-extrabold">Review</h1>
      <p className="text-ink-2 mt-1 text-sm">
        Versions publishers sent to review. Approving lets the studio publish it; asking for changes sends it back with
        your words.
      </p>

      <div className="mt-4 flex flex-wrap items-center gap-2">
        {FILTERS.map(([key, label]) => (
          <button
            key={key}
            type="button"
            className="chip"
            aria-pressed={key === status}
            onClick={() => setParams(key === 'in_review' ? {} : { status: key })}
          >
            {label}
            {key === 'in_review' && list.data ? ` · ${list.data.waiting}` : ''}
          </button>
        ))}
      </div>

      <div className="card mt-4 overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="text-ink-3 border-line bg-surface-2/60 border-b text-left text-xs">
            <tr>
              <th className="px-4 py-2">Game</th>
              <th className="px-4 py-2">Publisher</th>
              <th className="px-4 py-2">Version</th>
              <th className="px-4 py-2">Live</th>
              <th className="px-4 py-2">{status === 'in_review' ? 'Sent' : 'Decided'}</th>
            </tr>
          </thead>
          <tbody>
            {list.isLoading && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={5}>
                  Loading…
                </td>
              </tr>
            )}
            {list.isError && (
              <tr>
                <td className="text-bad px-4 py-6" colSpan={5}>
                  {list.error.message}
                </td>
              </tr>
            )}
            {list.data && rows.length === 0 && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={5}>
                  {status === 'in_review' ? 'Nothing is waiting for review.' : 'Nothing here.'}
                </td>
              </tr>
            )}
            {rows.map((r) => (
              <tr key={r.build.id} className="border-line border-b last:border-0">
                <td className="px-4 py-2">
                  <Link to={`/builds/${r.build.id}`} className="flex items-center gap-3 hover:underline">
                    <Tile src={r.game.icon} name={r.game.name} size="size-8" />
                    <span className="min-w-0">
                      <span className="block font-semibold">{r.game.name}</span>
                      <span className="text-ink-2 block font-mono text-xs" dir="ltr">
                        {r.game.id}
                      </span>
                    </span>
                  </Link>
                </td>
                <td className="px-4 py-2">
                  {r.publisher ? (
                    <Link to={`/publishers/${r.publisher.id}`} className="hover:underline">
                      {r.publisher.name}
                    </Link>
                  ) : (
                    '—'
                  )}
                </td>
                <td className="px-4 py-2">
                  <span className="flex items-center gap-1.5">
                    <span className="font-mono text-xs">{r.build.version}</span>
                    <BuildBadge status={r.build.status} />
                  </span>
                </td>
                <td className="px-4 py-2">
                  <span className="flex items-center gap-1.5">
                    <ListingBadge listing={r.game.listing} />
                    {r.game.version && <span className="text-ink-2 font-mono text-xs">{r.game.version}</span>}
                  </span>
                </td>
                <td className="text-ink-2 px-4 py-2 text-xs">
                  {when(status === 'in_review' ? r.build.submitted_at : (r.build.reviewed_at ?? r.build.submitted_at))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
