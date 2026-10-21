import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { api } from '../api.js'

const areas = ['', 'auth', 'players', 'games', 'rooms', 'admin']

// The audit log, newest first, one area or all.
export default function Activity() {
  const [area, setArea] = useState('')
  const [page, setPage] = useState(1)
  const q = useQuery({
    queryKey: ['audit', area, page],
    queryFn: () => api.get(`/v1/admin/audit?area=${area}&page=${page}&limit=50`),
  })
  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-extrabold">Activity</h1>
        <div className="flex gap-1">
          {areas.map((a) => (
            <button
              key={a || 'all'}
              type="button"
              className={`btn h-8 px-3 text-xs ${area === a ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => {
                setArea(a)
                setPage(1)
              }}
            >
              {a || 'all'} {q.data?.counts?.[a] != null ? `· ${q.data.counts[a]}` : ''}
            </button>
          ))}
        </div>
      </div>
      <div className="card mt-4 overflow-hidden">
        <table className="w-full text-sm">
          <thead className="text-ink-2 border-line border-b text-left text-xs uppercase">
            <tr>
              <th className="px-4 py-2">When</th>
              <th className="px-4 py-2">Area</th>
              <th className="px-4 py-2">Action</th>
              <th className="px-4 py-2">Actor</th>
              <th className="px-4 py-2">Subject</th>
              <th className="px-4 py-2">Detail</th>
            </tr>
          </thead>
          <tbody>
            {q.isLoading && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={6}>
                  Loading…
                </td>
              </tr>
            )}
            {q.isError && (
              <tr>
                <td className="text-bad px-4 py-6" colSpan={6}>
                  {q.error.message}
                </td>
              </tr>
            )}
            {q.data?.entries?.map((e) => (
              <tr key={e.id} className="border-line border-b align-top last:border-0">
                <td className="text-ink-2 px-4 py-2 text-xs whitespace-nowrap">{e.at.replace('T', ' ').slice(0, 19)}</td>
                <td className="px-4 py-2 text-xs">{e.area}</td>
                <td className="px-4 py-2 font-semibold">{e.action}</td>
                <td className="px-4 py-2 font-mono text-xs">{e.actor}</td>
                <td className="px-4 py-2 font-mono text-xs">{e.subject}</td>
                <td className="text-ink-2 px-4 py-2 font-mono text-xs">{JSON.stringify(e.detail)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mt-3 flex items-center gap-2 text-sm">
        <button type="button" className="btn btn-secondary h-8 px-3 text-xs" disabled={page <= 1} onClick={() => setPage(page - 1)}>
          Newer
        </button>
        <span className="text-ink-2">page {page}</span>
        <button
          type="button"
          className="btn btn-secondary h-8 px-3 text-xs"
          disabled={!q.data || q.data.entries.length < 50}
          onClick={() => setPage(page + 1)}
        >
          Older
        </button>
      </div>
    </div>
  )
}
