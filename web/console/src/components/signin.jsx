import { useState } from 'react'

import { docsUrl, listingGuide, siteUrl } from '../lib/links.js'
import { builtAt, versionLabel } from '../lib/version.js'
import { devAdmin, devSignIn, passwordSignIn, passwordSignInWith, publisherSignIn, signIn, ssoConfigured } from '../sso.js'

const LAST_NAME = 'ga.console.name'
const LAST_MODE = 'ga.console.mode'
const LAST_EMAIL = 'ga.console.publisher-email'

function remembered(key, fallback = '') {
  try {
    return localStorage.getItem(key) || fallback
  } catch {
    return fallback
  }
}

function remember(key, value) {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* a private window: it is asked again next time */
  }
}

function lastName() {
  return remembered(LAST_NAME)
}

// The one card a signed-out admin sees, holding only the ways in this build
// has: the identity provider, the console's password, and a laptop's dev
// sign-in. The name the password form asks for is what the activity log
// shows, so this browser remembers it.
export default function SignIn({ note, onSignedIn }) {
  // Admins and publishers sign in on the same card: the switch says which,
  // and this browser remembers the last one.
  const [mode, setMode] = useState(() => (remembered(LAST_MODE) === 'publisher' ? 'publisher' : 'admin'))
  const [email, setEmail] = useState(() => remembered(LAST_EMAIL))
  const [name, setName] = useState(lastName)
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const none = !ssoConfigured && !passwordSignIn && !devAdmin

  async function attempt(run) {
    setBusy(true)
    setError(null)
    try {
      await run()
      onSignedIn()
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  function withPassword(e) {
    e.preventDefault()
    attempt(async () => {
      await passwordSignInWith(name.trim(), password)
      try {
        localStorage.setItem(LAST_NAME, name.trim())
      } catch {
        /* a private window: it is asked again next time */
      }
    })
  }

  function asDev(e) {
    e.preventDefault()
    attempt(() => devSignIn(name.trim() || 'dev'))
  }

  function asPublisher(e) {
    e.preventDefault()
    attempt(async () => {
      await publisherSignIn(email.trim(), password)
      remember(LAST_EMAIL, email.trim())
    })
  }

  function pick(next) {
    setMode(next)
    setError(null)
    setPassword('')
    remember(LAST_MODE, next)
  }

  return (
    <div className="grid min-h-[100dvh] grid-rows-[auto_1fr] lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)] lg:grid-rows-1">
      <aside className="bg-rail relative isolate flex flex-col justify-between overflow-hidden px-6 py-8 sm:px-10 lg:p-14">
        <div
          aria-hidden="true"
          className="absolute inset-0 -z-10 opacity-25"
          style={{ backgroundImage: 'url(/brand/pattern-tile-dark.svg)', backgroundSize: '128px' }}
        />
        <div aria-hidden="true" className="from-rail via-rail/85 to-rail/40 absolute inset-0 -z-10 bg-linear-to-t" />
        <img src="/brand/lockup-dark.svg" alt="گعدة, سهرة تجمعنا" className="w-40 sm:w-48 lg:w-64" />
        <div className="mt-10 hidden max-w-md lg:block">
          <p className="font-display text-rail-ink text-3xl leading-tight font-extrabold">
            {mode === 'publisher' ? 'Make games for گعدة' : 'The console'}
          </p>
          <p className="text-rail-ink-2 mt-3 text-base">
            {mode === 'publisher'
              ? 'Register your game, try it on real phones with the testers you invite, and send it to review.'
              : 'Games, players, rooms and tournaments, run from one place.'}
          </p>
        </div>
      </aside>

      <main className="flex items-start justify-center px-6 py-10 sm:px-10 lg:items-center">
        <div className="w-full max-w-sm">
          <div className="bg-surface-2 mb-7 inline-flex rounded-lg p-1" role="group" aria-label="Who is signing in">
            {[
              ['admin', 'Admin'],
              ['publisher', 'Publisher'],
            ].map(([key, label]) => (
              <button
                key={key}
                type="button"
                aria-pressed={mode === key}
                className={`rounded-md px-4 py-1.5 text-sm font-semibold ${
                  mode === key ? 'bg-surface text-ink shadow-sm' : 'text-ink-2 hover:text-ink'
                }`}
                onClick={() => pick(key)}
              >
                {label}
              </button>
            ))}
          </div>
          <h1 className="text-3xl font-extrabold">Sign in</h1>
          <p className="text-ink-2 mt-2 text-sm">
            {mode === 'publisher'
              ? 'With the email your studio was given. A first sign-in starts from the link an admin sent you.'
              : passwordSignIn
                ? 'Your name goes on everything you change here.'
                : 'Sign in with your admin account.'}
          </p>
          {note && (
            <p className="border-warn/40 bg-warn/10 text-warn mt-5 rounded-lg border px-3 py-2 text-sm" role="status">
              {note}
            </p>
          )}

          {mode === 'publisher' && (
            <form onSubmit={asPublisher} className="mt-7 flex flex-col gap-4">
              <label className="flex flex-col gap-1.5">
                <span className="label">Email</span>
                <input
                  className="field h-11"
                  type="email"
                  dir="ltr"
                  autoComplete="username"
                  maxLength={254}
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </label>
              <label className="flex flex-col gap-1.5">
                <span className="label">Password</span>
                <input
                  className="field h-11"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <button type="submit" className="btn btn-primary mt-2 h-11 w-full" disabled={busy || !email.trim() || !password}>
                {busy ? 'Signing in…' : 'Sign in'}
              </button>
              <p className="text-ink-3 text-xs">Forgot the password? Ask the گعدة team for a new sign-in link.</p>
              <p className="text-ink-2 border-line border-t pt-4 text-sm">
                New to گعدة?{' '}
                <a href={listingGuide} target="_blank" rel="noopener noreferrer" className="text-accent-text font-semibold hover:underline">
                  Read how a game gets listed
                </a>
                , then ask us for a studio account at{' '}
                <a href="mailto:ga3da.fun@gmail.com" className="font-semibold hover:underline">
                  ga3da.fun@gmail.com
                </a>
                .
              </p>
            </form>
          )}

          {mode === 'admin' && ssoConfigured && (
            <button type="button" className="btn btn-primary mt-7 h-11 w-full" disabled={busy} onClick={() => signIn()}>
              Sign in with your account
            </button>
          )}

          {mode === 'admin' && passwordSignIn && (
            <form onSubmit={withPassword} className="mt-7 flex flex-col gap-4">
              {ssoConfigured && <Or />}
              <label className="flex flex-col gap-1.5">
                <span className="label">Your name</span>
                <input
                  className="field h-11"
                  autoComplete="username"
                  maxLength={120}
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label className="flex flex-col gap-1.5">
                <span className="label">Password</span>
                <input
                  className="field h-11"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <button type="submit" className="btn btn-primary mt-2 h-11 w-full" disabled={busy || !name.trim() || !password}>
                {busy ? 'Signing in…' : 'Sign in'}
              </button>
            </form>
          )}

          {mode === 'admin' && devAdmin && (
            <form onSubmit={asDev} className="mt-7 flex flex-col gap-3">
              {(ssoConfigured || passwordSignIn) && <Or label="or, on a laptop" />}
              <label className="flex flex-col gap-1.5">
                <span className="label">Dev sign-in (laptop only)</span>
                <input className="field h-11" placeholder="your name" value={name} onChange={(e) => setName(e.target.value)} />
              </label>
              <button type="submit" className="btn btn-secondary h-11 w-full" disabled={busy}>
                {busy ? 'Signing in…' : 'Sign in as dev'}
              </button>
            </form>
          )}

          {mode === 'admin' && none && (
            <p className="text-ink-3 mt-7 text-xs">
              This build has no way in: set VITE_SSO_ISSUER and VITE_SSO_CLIENT_ID, or VITE_PASSWORD_SIGNIN.
            </p>
          )}
          {error && (
            <p className="text-bad mt-4 text-sm" role="alert">
              {error}
            </p>
          )}
          <nav aria-label="Elsewhere" className="text-ink-2 mt-10 flex flex-wrap gap-x-5 gap-y-1 text-xs font-semibold">
            <a href={docsUrl} target="_blank" rel="noopener noreferrer" className="hover:text-ink hover:underline">
              Developer docs
            </a>
            <a href={siteUrl} target="_blank" rel="noopener noreferrer" className="hover:text-ink hover:underline">
              ga3da.fun
            </a>
          </nav>
          <p className="text-ink-3 mt-3 font-mono text-[11px]" title={builtAt}>
            Console {versionLabel}
          </p>
        </div>
      </main>
    </div>
  )
}

// A divider between two ways in.
function Or({ label = 'or' }) {
  return (
    <div className="text-ink-3 flex items-center gap-3 text-xs">
      <span className="bg-line h-px flex-1" />
      {label}
      <span className="bg-line h-px flex-1" />
    </div>
  )
}
