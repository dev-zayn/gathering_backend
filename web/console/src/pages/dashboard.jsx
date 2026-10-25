import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ArrowRight, DoorOpen, Gamepad2, Users } from 'lucide-react'

import { api } from '../api.js'

// The first page: the numbers the lists cannot count themselves, by what
// they count. Each group's link opens the list behind it.
const groups = [
  {
    title: 'Players',
    to: '/players',
    icon: Users,
    span: 'lg:col-span-12',
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
    icon: DoorOpen,
    span: 'lg:col-span-8',
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
    icon: Gamepad2,
    span: 'lg:col-span-4',
    cards: [['games', 'Published']],
  },
]

// A dotted key reads into the answer: rooms.lobby is answer.rooms.lobby.
const read = (data, key) => key.split('.').reduce((v, k) => v?.[k], data)

export default function Dashboard() {
  const q = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get('/v1/admin/dashboard'), refetchInterval: 30_000 })
  return (
    <div>
      <h1 className="text-2xl font-extrabold">Dashboard</h1>
      <p className="text-ink-2 mt-1 text-sm">Today is counted from midnight UTC.</p>
      <div className="mt-6 grid grid-cols-1 gap-4 lg:grid-cols-12">
        {groups.map((g) => (
          <section key={g.title} className={`card flex flex-col overflow-hidden ${g.span}`}>
            <div className="border-line flex items-center justify-between gap-3 border-b px-5 py-3.5">
              <h2 className="flex items-center gap-2.5 text-base font-extrabold">
                <span className="bg-accent/15 text-accent-text inline-flex size-8 items-center justify-center rounded-lg">
                  <g.icon className="size-4" strokeWidth={2} />
                </span>
                {g.title}
              </h2>
              <Link to={g.to} className="text-ink-2 hover:text-ink inline-flex items-center gap-1 text-xs font-semibold">
                Open {g.title.toLowerCase()} <ArrowRight className="size-3.5 rtl:rotate-180" />
              </Link>
            </div>
            <dl className={`bg-line grid flex-1 gap-px ${g.cards.length > 1 ? 'grid-cols-2 sm:grid-cols-4' : 'grid-cols-1'}`}>
              {g.cards.map(([key, label]) => (
                <div key={key} className="bg-surface px-5 py-4">
                  <dt className="text-ink-2 text-sm font-medium">{label}</dt>
                  <dd className="mt-2 font-mono text-3xl leading-none font-semibold tracking-tight tabular-nums">
                    {q.isLoading ? (
                      <span className="bg-surface-2 inline-block h-7 w-14 animate-pulse rounded-md align-middle" />
                    ) : q.isError ? (
                      '—'
                    ) : (
                      (read(q.data, key) ?? '—')
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
      {q.isError && <p className="text-bad mt-3 text-sm">{q.error.message}</p>}
    </div>
  )
}
