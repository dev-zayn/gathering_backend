import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '../api.js'

const presenceTone = { online: 'text-good', in_room: 'text-info', offline: 'text-ink-3' }

// Players: find by handle, read the record, switch one off or on.
export default function Players() {
  const [q, setQ] = useState('')
  const queries = useQueryClient()
  const list = useQuery({
    queryKey: ['players', q],
    queryFn: () => api.get(`/v1/admin/players?q=${encodeURIComponent(q)}&limit=100`),
  })
  const flip = useMutation({
    mutationFn: ({ id, disabled }) => api.post(`/v1/admin/players/${id}/${disabled ? 'enable' : 'disable'}`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['players'] }),
  })

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-xl font-extrabold">Players</h1>
        <input
          className="field max-w-xs"
          placeholder="@handle"
          value={q}
          onChange={(e) => setQ(e.target.value.trim())}
          aria-label="Find by handle"
        />
      </div>
      <div className="card mt-4 overflow-hidden">
        <table className="w-full text-sm">
          <thead className="text-ink-2 border-line border-b text-left text-xs uppercase">
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
                <td className="px-4 py-2 font-semibold">{p.nickname}</td>
                <td className="px-4 py-2 font-mono text-xs">@{p.handle}</td>
                <td className="px-4 py-2 font-mono text-xs" dir="ltr">
                  {p.phone || '—'}
                </td>
                <td className={`px-4 py-2 text-xs font-semibold ${presenceTone[p.presence] ?? ''}`}>{p.presence}</td>
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
    </div>
  )
}
