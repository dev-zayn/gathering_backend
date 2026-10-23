import { NavLink, Outlet } from 'react-router-dom'
import { Gamepad2, LayoutDashboard, Users, DoorOpen, Trophy, Bell, ScrollText, ShieldCheck, LogOut } from 'lucide-react'

import { useMe } from '../lib/me.js'
import BrandMark from './brand-mark.jsx'
import { signOut } from '../sso.js'

// The frame every page sits in: the rail of sections and who is signed in.
const sections = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard, end: true },
  { to: '/games', label: 'Games', icon: Gamepad2 },
  { to: '/players', label: 'Players', icon: Users },
  { to: '/rooms', label: 'Rooms', icon: DoorOpen },
  { to: '/tournaments', label: 'Tournaments', icon: Trophy },
  { to: '/notifications', label: 'Notifications', icon: Bell },
  { to: '/activity', label: 'Activity', icon: ScrollText },
  { to: '/admins', label: 'Admins', icon: ShieldCheck },
]

export default function Shell() {
  const me = useMe()
  return (
    <div className="min-h-screen lg:grid lg:grid-cols-[240px_1fr]">
      <aside className="border-line bg-surface flex flex-col gap-1 border-e p-3">
        <div className="mb-3 flex items-center gap-2 px-2 pt-1">
          <span className="bg-accent text-accent-ink inline-flex size-8 items-center justify-center rounded-lg">
            <BrandMark className="w-[18px]" />
          </span>
          <span className="text-sm font-bold">Gathering console</span>
        </div>
        {sections.map(({ to, label, icon: Icon, end }) => (
          <NavLink key={to} to={to} end={end} className="navkey">
            <Icon className="size-4" />
            {label}
          </NavLink>
        ))}
        <div className="mt-auto flex items-center justify-between px-2 pt-3 text-xs">
          <span className="text-ink-2 truncate">{me?.name || me?.id}</span>
          <button type="button" className="text-ink-2 hover:text-ink inline-flex items-center gap-1" onClick={signOut}>
            <LogOut className="size-3.5" /> Sign out
          </button>
        </div>
      </aside>
      <main className="p-6">
        <Outlet />
      </main>
    </div>
  )
}
