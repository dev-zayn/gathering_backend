import { useState } from 'react'

import { publisherSetup } from '../sso.js'

// The one-time link an admin made for a publisher (docs/PUBLISHERS.md):
// /setup#<token>. The token rides in the fragment, so it never reaches a
// server's log; choosing the password spends it and signs the studio in.
export default function Setup({ onSignedIn }) {
  const [token] = useState(() => decodeURIComponent(location.hash.replace(/^#/, '')).trim())
  const [password, setPassword] = useState('')
  const [again, setAgain] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const short = password.length > 0 && password.length < 10
  const differs = again.length > 0 && again !== password

  async function submit(e) {
    e.preventDefault()
    if (short || differs || !password) return
    setBusy(true)
    setError(null)
    try {
      await publisherSetup(token, password)
      history.replaceState(null, '', '/')
      onSignedIn()
    } catch (err) {
      setError(
        err.data?.error === 'invalid_setup'
          ? 'This link has been used or has expired. Ask the گعدة team for a new one.'
          : err.message,
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid min-h-[100dvh] place-items-center px-6 py-10">
      <div className="w-full max-w-sm">
        <img src="/brand/wordmark-amber.svg" alt="گعدة" className="h-[46px] w-auto" />
        <h1 className="mt-8 text-3xl font-extrabold">Choose your password</h1>
        <p className="text-ink-2 mt-2 text-sm">
          Your studio signs in to the گعدة console with its email and this password. The link works once.
        </p>
        {!token ? (
          <p className="text-bad mt-6 text-sm" role="alert">
            This page needs the whole link you were sent, with the part after the #.
          </p>
        ) : (
          <form onSubmit={submit} className="mt-7 flex flex-col gap-4">
            <label className="flex flex-col gap-1.5">
              <span className="label">Password</span>
              <input
                className="field h-11"
                type="password"
                autoComplete="new-password"
                autoFocus
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <span className={`text-xs ${short ? 'text-warn' : 'text-ink-3'}`}>At least 10 characters.</span>
            </label>
            <label className="flex flex-col gap-1.5">
              <span className="label">The same again</span>
              <input
                className="field h-11"
                type="password"
                autoComplete="new-password"
                required
                value={again}
                onChange={(e) => setAgain(e.target.value)}
              />
              {differs && <span className="text-warn text-xs">The two do not match.</span>}
            </label>
            <button
              type="submit"
              className="btn btn-primary mt-2 h-11 w-full"
              disabled={busy || short || differs || !password || !again}
            >
              {busy ? 'Saving…' : 'Save and sign in'}
            </button>
            {error && (
              <p className="text-bad text-sm" role="alert">
                {error}
              </p>
            )}
          </form>
        )}
      </div>
    </div>
  )
}
