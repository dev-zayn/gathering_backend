import { useQuery } from '@tanstack/react-query'

import { api } from '../api.js'

const cards = [
  ['players', 'Players'],
  ['online', 'Online now'],
  ['active', 'Active, 7 days'],
  ['new_today', 'New today'],
  ['games', 'Games'],
  ['rooms_live', 'Rooms live'],
]

// The first page: the numbers the lists cannot count themselves.
export default function Dashboard() {
  const q = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get('/v1/admin/dashboard') })
  return (
    <div>
      <h1 className="text-xl font-extrabold">Dashboard</h1>
      <div className="mt-4 grid gap-3 sm:grid-cols-3">
        {cards.map(([key, label]) => (
          <div key={key} className="card p-4">
            <div className="text-ink-2 text-xs font-semibold uppercase">{label}</div>
            <div className="mt-1 text-2xl font-light tabular-nums">
              {q.isLoading ? '…' : q.isError ? '—' : (q.data?.[key] ?? '—')}
            </div>
          </div>
        ))}
      </div>
      {q.isError && <p className="text-bad mt-3 text-sm">{q.error.message}</p>}
    </div>
  )
}
