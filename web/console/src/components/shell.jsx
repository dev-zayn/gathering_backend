import { useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import {
  Bell,
  Building2,
  ClipboardCheck,
  DoorOpen,
  Flag,
  Gamepad2,
  LayoutDashboard,
  LogOut,
  Menu,
  Monitor,
  Moon,
  ScrollText,
  ShieldCheck,
  Sun,
  Trophy,
  Users,
  X,
} from 'lucide-react'

import { useMe } from '../lib/me.js'
import { applyTheme, nextTheme, savedTheme } from '../lib/theme.js'
import { builtAt, versionLabel } from '../lib/version.js'
import { signOut } from '../sso.js'

// The frame every page sits in: the rail of sections on Navy, with the
// wordmark at its head and who is signed in at its foot, then which build
// this is. On a phone the rail
// is a drawer behind the bar's menu key.
const adminGroups = [
  [
    { to: '/', label: 'Dashboard', icon: LayoutDashboard, end: true },
    { to: '/games', label: 'Games', icon: Gamepad2 },
    { to: '/review', label: 'Review', icon: ClipboardCheck },
    { to: '/players', label: 'Players', icon: Users },
    { to: '/reports', label: 'Reports', icon: Flag },
    { to: '/rooms', label: 'Rooms', icon: DoorOpen },
    { to: '/tournaments', label: 'Tournaments', icon: Trophy },
    { to: '/notifications', label: 'Notifications', icon: Bell },
  ],
  [
    { to: '/publishers', label: 'Publishers', icon: Building2 },
    { to: '/activity', label: 'Activity', icon: ScrollText },
    { to: '/admins', label: 'Admins', icon: ShieldCheck },
  ],
]

// The brand's wordmark, never set in a font; 96 px wide at the least.
function Wordmark({ className = '' }) {
  return <img src="/brand/wordmark-amber.svg" alt="گعدة" className={`h-[46px] w-auto ${className}`} />
}

// The shell is the admin's by default; a publisher's passes its own rail,
// the area's name under the wordmark and the way it signs out.
export default function Shell({ groups = adminGroups, area = 'Console', onSignOut = signOut }) {
  const [open, setOpen] = useState(false)
  const close = () => setOpen(false)

  return (
    <div className="min-h-[100dvh] lg:grid lg:grid-cols-[264px_minmax(0,1fr)]">
      <header className="bg-rail sticky top-0 z-30 flex h-16 items-center justify-between px-4 lg:hidden">
        <Wordmark />
        <button
          type="button"
          className="text-rail-ink inline-flex size-10 items-center justify-center rounded-lg hover:bg-white/5"
          aria-label="Open the menu"
          aria-expanded={open}
          onClick={() => setOpen(true)}
        >
          <Menu className="size-5" />
        </button>
      </header>

      {open && <div className="bg-scrim fixed inset-0 z-40 lg:hidden" onClick={close} aria-hidden="true" />}

      <aside
        className={`bg-rail border-rail-line fixed inset-y-0 start-0 z-50 flex w-[264px] flex-col border-e px-3 pt-5 pb-4 transition-transform duration-200 lg:sticky lg:top-0 lg:z-auto lg:h-[100dvh] lg:translate-x-0 ${
          open ? 'translate-x-0' : '-translate-x-full rtl:translate-x-full'
        }`}
      >
        <div className="flex items-center justify-between px-3">
          <div className="flex items-end gap-2.5">
            <Wordmark />
            <span className="text-rail-ink-2 mb-1.5 text-xs font-semibold">{area}</span>
          </div>
          <button
            type="button"
            className="text-rail-ink-2 hover:text-rail-ink inline-flex size-9 items-center justify-center rounded-lg lg:hidden"
            aria-label="Close the menu"
            onClick={close}
          >
            <X className="size-5" />
          </button>
        </div>

        <nav className="mt-7 flex flex-1 flex-col gap-1 overflow-y-auto px-3" aria-label="Sections">
          {groups.map((group, i) => (
            <div key={i} className={`flex flex-col gap-1 ${i > 0 ? 'border-rail-line mt-3 border-t pt-3' : ''}`}>
              {group.map(({ to, label, icon: Icon, end }) => (
                <NavLink key={to} to={to} end={end} className="navkey" onClick={close}>
                  <Icon className="size-[18px]" strokeWidth={1.75} />
                  {label}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>

        <Account onSignOut={onSignOut} />
        <p className="text-rail-ink-2 mx-3 mt-3 font-mono text-[11px]" title={builtAt}>
          Console {versionLabel}
        </p>
      </aside>

      <main className="min-w-0 px-4 py-6 sm:px-6 lg:px-10 lg:py-9">
        <div className="mx-auto max-w-[1240px]">
          <Outlet />
        </div>
      </main>
    </div>
  )
}

const themeIcon = { system: Monitor, light: Sun, dark: Moon }
const themeName = { system: 'the system’s', light: 'light', dark: 'dark' }

// Who is signed in, the theme switch, and the way out.
function Account({ onSignOut }) {
  const me = useMe()
  const [theme, setTheme] = useState(savedTheme)
  const ThemeIcon = themeIcon[theme]
  const name = me?.name || me?.id || 'Admin'
  const role =
    me?.kind === 'publisher'
      ? 'Publisher'
      : me?.roles?.includes('admin')
        ? 'Admin'
        : me?.roles?.[0]
          ? me.roles[0]
          : 'Signed in'

  function cycle() {
    const next = nextTheme[theme]
    applyTheme(next)
    setTheme(next)
  }

  return (
    <div className="border-rail-line mx-3 mt-3 flex items-center gap-3 border-t pt-4">
      <span className="bg-accent text-accent-ink font-display inline-flex size-9 shrink-0 items-center justify-center rounded-full text-sm font-extrabold">
        {name.slice(0, 1).toUpperCase()}
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-rail-ink truncate text-sm font-semibold">{name}</div>
        <div className="text-rail-ink-2 truncate text-xs capitalize">{role}</div>
      </div>
      <button
        type="button"
        className="text-rail-ink-2 hover:text-rail-ink inline-flex size-8 items-center justify-center rounded-lg hover:bg-white/5"
        title={`Colours: ${themeName[theme]}. Click to change.`}
        aria-label={`Colours: ${themeName[theme]}`}
        onClick={cycle}
      >
        <ThemeIcon className="size-4" />
      </button>
      <button
        type="button"
        className="text-rail-ink-2 hover:text-rail-ink inline-flex size-8 items-center justify-center rounded-lg hover:bg-white/5"
        title="Sign out"
        aria-label="Sign out"
        onClick={onSignOut}
      >
        <LogOut className="size-4" />
      </button>
    </div>
  )
}
