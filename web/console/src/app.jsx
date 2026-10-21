import { useEffect, useState } from 'react'
import { BrowserRouter, Route, Routes } from 'react-router-dom'

import { api, SIGNED_OUT } from './api.js'
import { Me } from './lib/me.js'
import { finish, signIn } from './sso.js'
import Shell from './components/shell.jsx'
import SignIn from './components/signin.jsx'
import Dashboard from './pages/dashboard.jsx'
import Games from './pages/games.jsx'
import Game from './pages/game.jsx'
import Players from './pages/players.jsx'
import Admins from './pages/admins.jsx'
import Activity from './pages/activity.jsx'
import Soon from './pages/soon.jsx'

// Boot: finish a sign-in callback if this is one, then ask who is signed in.
// A 401 shows the sign-in card, a 403 the card with a note, anything else an
// offline screen with a retry.
export default function App() {
  const [state, setState] = useState({ phase: 'booting' })

  async function boot() {
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
      setState({ phase: 'in', me })
    } catch (e) {
      if (e.status === 401) setState({ phase: 'out' })
      else if (e.status === 403) setState({ phase: 'out', note: 'Your account has no role here. Sign in as somebody else.' })
      else setState({ phase: 'offline', message: e.message })
    }
  }

  useEffect(() => {
    boot()
    const onSignedOut = () => {
      const last = Number(sessionStorage.getItem('ga.console.signed-in-at') || 0)
      if (Date.now() - last < 60_000) setState({ phase: 'out', note: 'The session ended right after signing in. Try again.' })
      else signIn(location.pathname)
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

  return (
    <Me.Provider value={state.me}>
      <BrowserRouter>
        <Routes>
          <Route element={<Shell />}>
            <Route index element={<Dashboard />} />
            <Route path="games" element={<Games />} />
            <Route path="games/:id" element={<Game />} />
            <Route path="players" element={<Players />} />
            <Route path="rooms" element={<Soon title="Rooms" milestone="M8" />} />
            <Route path="notifications" element={<Soon title="Notifications" milestone="M8" />} />
            <Route path="activity" element={<Activity />} />
            <Route path="admins" element={<Admins />} />
            <Route path="*" element={<Soon title="Not found" milestone="—" />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </Me.Provider>
  )
}
