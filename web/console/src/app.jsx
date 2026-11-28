import { useEffect, useRef, useState } from 'react'
import { BrowserRouter, Route, Routes } from 'react-router-dom'

import { api, SIGNED_OUT } from './api.js'
import { Me } from './lib/me.js'
import { finish, publisherSignOut, signIn, ssoConfigured } from './sso.js'
import Shell from './components/shell.jsx'
import { listingGuide } from './lib/links.js'
import { studioGroups } from './lib/rails.js'
import SignIn from './components/signin.jsx'
import Setup from './pages/setup.jsx'
import Publishers from './pages/publishers.jsx'
import Publisher from './pages/publisher.jsx'
import Review from './pages/review.jsx'
import Build from './pages/build.jsx'
import StudioGames from './pages/studio/games.jsx'
import StudioGame from './pages/studio/game.jsx'
import StudioAccount from './pages/studio/account.jsx'
import Dashboard from './pages/dashboard.jsx'
import Games from './pages/games.jsx'
import Game from './pages/game.jsx'
import Categories from './pages/categories.jsx'
import Players from './pages/players.jsx'
import Reports from './pages/reports.jsx'
import Rooms from './pages/rooms.jsx'
import Room from './pages/room.jsx'
import Tournaments from './pages/tournaments.jsx'
import Tournament from './pages/tournament.jsx'
import Notifications from './pages/notifications.jsx'
import Admins from './pages/admins.jsx'
import Activity from './pages/activity.jsx'

// Boot: finish a sign-in callback if this is one, then ask who is signed in:
// an admin, or else a publisher (docs/PUBLISHERS.md), whose session is the
// same cookie holding another kind of token. A 401 shows the sign-in card, a
// 403 the card with a note, anything else an offline screen with a retry. A
// publisher's one-time link is its own page, whoever is signed in.
export default function App() {
  const [state, setState] = useState(() => (location.pathname === '/setup' ? { phase: 'setup' } : { phase: 'booting' }))
  const kind = useRef('admin')

  async function boot() {
    // The link's page asks nobody who is signed in: it signs somebody in.
    if (location.pathname === '/setup') return
    if (location.pathname === '/auth/callback') {
      try {
        const returnTo = await finish()
        history.replaceState(null, '', returnTo)
      } catch (e) {
        setState({ phase: 'out', note: e.message })
        history.replaceState(null, '', '/')
        return
      }
    }
    try {
      const me = await api.get('/v1/admin/me', { quiet: true })
      kind.current = 'admin'
      setState({ phase: 'in', me: { ...me, kind: 'admin' } })
      return
    } catch (e) {
      const another = e.status === 403 && e.data?.error === 'wrong_token'
      if (e.status !== 401 && !another) {
        if (e.status === 403) setState({ phase: 'out', note: 'Your account has no role here. Sign in as somebody else.' })
        else setState({ phase: 'offline', message: e.message })
        return
      }
    }
    try {
      const me = await api.get('/v1/publisher/me', { quiet: true })
      kind.current = 'publisher'
      setState({ phase: 'studio', me: { ...me, kind: 'publisher' } })
    } catch (e) {
      if (e.status === 401 || e.data?.error === 'wrong_token') setState({ phase: 'out' })
      else if (e.data?.error === 'publisher_disabled')
        setState({ phase: 'out', note: 'Your studio’s account is switched off. Ask the گعدة team.' })
      else setState({ phase: 'offline', message: e.message })
    }
  }

  useEffect(() => {
    boot()
    // A session gone mid-use: back to the provider when there is one, which
    // usually signs straight back in; otherwise the card, saying why. A
    // publisher never goes to the admins' provider.
    const onSignedOut = () => {
      const last = Number(sessionStorage.getItem('ga.console.signed-in-at') || 0)
      if (Date.now() - last < 60_000) setState({ phase: 'out', note: 'The session ended right after signing in. Try again.' })
      else if (ssoConfigured && kind.current === 'admin') signIn(location.pathname)
      else setState({ phase: 'out', note: 'Your session ended. Sign in again.' })
    }
    window.addEventListener(SIGNED_OUT, onSignedOut)
    return () => window.removeEventListener(SIGNED_OUT, onSignedOut)
  }, [])

  if (state.phase === 'booting') return <div className="text-ink-3 grid min-h-screen place-items-center text-sm">Loading…</div>
  if (state.phase === 'offline')
    return (
      <div className="grid min-h-screen place-items-center p-6">
        <div className="card max-w-sm p-6 text-center">
          <p className="text-sm">{state.message}</p>
          <button
            type="button"
            className="btn btn-secondary mt-4"
            onClick={() => {
              setState({ phase: 'booting' })
              boot()
            }}
          >
            Try again
          </button>
        </div>
      </div>
    )
  if (state.phase === 'setup')
    return (
      <Setup
        onSignedIn={() => {
          sessionStorage.setItem('ga.console.signed-in-at', String(Date.now()))
          boot()
        }}
      />
    )
  if (state.phase === 'out')
    return (
      <SignIn
        note={state.note}
        onSignedIn={() => {
          sessionStorage.setItem('ga.console.signed-in-at', String(Date.now()))
          boot()
        }}
      />
    )

  if (state.phase === 'studio')
    return (
      <Me.Provider value={state.me}>
        <BrowserRouter>
          <Routes>
            <Route element={<Shell groups={studioGroups} area="Publisher" onSignOut={publisherSignOut} docs={listingGuide} />}>
              <Route index element={<StudioGames />} />
              <Route path="games/:id" element={<StudioGame />} />
              <Route path="account" element={<StudioAccount />} />
              <Route path="*" element={<NotFound />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </Me.Provider>
    )

  return (
    <Me.Provider value={state.me}>
      <BrowserRouter>
        <Routes>
          <Route element={<Shell />}>
            <Route index element={<Dashboard />} />
            <Route path="games" element={<Games />} />
            <Route path="games/:id" element={<Game />} />
            <Route path="categories" element={<Categories />} />
            <Route path="review" element={<Review />} />
            <Route path="builds/:id" element={<Build />} />
            <Route path="publishers" element={<Publishers />} />
            <Route path="publishers/:id" element={<Publisher />} />
            <Route path="players" element={<Players />} />
            <Route path="reports" element={<Reports />} />
            <Route path="rooms" element={<Rooms />} />
            <Route path="rooms/:id" element={<Room />} />
            <Route path="tournaments" element={<Tournaments />} />
            <Route path="tournaments/:id" element={<Tournament />} />
            <Route path="notifications" element={<Notifications />} />
            <Route path="activity" element={<Activity />} />
            <Route path="admins" element={<Admins />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </Me.Provider>
  )
}

// An address no section answers.
function NotFound() {
  return (
    <div>
      <h1 className="text-xl font-extrabold">Not found</h1>
      <p className="text-ink-2 mt-2 text-sm">No page here. The rail on the side has every section.</p>
    </div>
  )
}
