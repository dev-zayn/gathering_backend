import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { api } from '../api.js'

// The first page: the numbers the lists cannot count themselves, by what
// they count. Each group's link opens the list behind it.
const groups = [
  {
    title: 'Players',
    to: '/players',
    cards: [
      ['players', 'Players'],
      ['online', 'Online now'],
      ['active', 'Active, 7 days'],
      ['new_today', 'New today'],
    ],
  },
  {
    title: 'Rooms',
    to: '/rooms',
    cards: [
      ['rooms.lobby', 'In the lobby'],
      ['rooms.playing', 'Playing'],
      ['rooms.today', 'Made today'],
      ['rounds_today', 'Rounds today'],
    ],
  },
  {
    title: 'Games',
    to: '/games',
    cards: [['games', 'Published']],
  },
]

// A dotted key reads into the answer: rooms.lobby is answer.rooms.lobby.
const read = (data, key) => key.split('.').reduce((v, k) => v?.[k], data)

export default function Dashboard() {
  const q = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get('/v1/admin/dashboard'), refetchInterval: 30_000 })
  return (
    <div>
      <h1 className="text-xl font-extrabold">Dashboard</h1>
      <p className="text-ink-2 mt-1 text-sm">Today is counted from midnight UTC.</p>
      {groups.map((g) => (
        <section key={g.title} className="mt-5">
          <div className="flex items-baseline justify-between">
            <h2 className="text-sm font-extrabold">{g.title}</h2>
            <Link to={g.to} className="text-ink-2 hover:text-ink text-xs font-semibold">
              Open {g.title.toLowerCase()} →
            </Link>
          </div>
          <div className="mt-2 grid grid-cols-2 gap-3 lg:grid-cols-4">
            {g.cards.map(([key, label]) => (
              <div key={key} className="card p-4">
                <div className="text-ink-2 text-xs font-semibold uppercase">{label}</div>
                <div className="mt-1 text-2xl font-light tabular-nums">
                  {q.isLoading ? '…' : q.isError ? '—' : (read(q.data, key) ?? '—')}
                </div>
              </div>
            ))}
          </div>
        </section>
      ))}
      {q.isError && <p className="text-bad mt-3 text-sm">{q.error.message}</p>}
    </div>
  )
}
