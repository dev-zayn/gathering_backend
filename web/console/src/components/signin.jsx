import { useState } from 'react'

import { devAdmin, devSignIn, signIn, ssoConfigured } from '../sso.js'

// The one card a signed-out admin sees. The dev sign-in is a laptop's.
export default function SignIn({ note, onSignedIn }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  async function dev(e) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await devSignIn(name.trim() || 'dev')
      onSignedIn()
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid min-h-screen place-items-center p-6">
      <div className="card w-full max-w-sm p-6">
        <h1 className="text-lg font-extrabold">Gathering console</h1>
        <p className="text-ink-2 mt-1 text-sm">Sign in with your admin account.</p>
        {note && <p className="text-warn mt-3 text-sm">{note}</p>}
        <button type="button" className="btn btn-primary mt-5 w-full" disabled={!ssoConfigured} onClick={() => signIn()}>
          Sign in
        </button>
        {!ssoConfigured && (
          <p className="text-ink-3 mt-2 text-xs">
            No identity provider is configured for this build (VITE_SSO_ISSUER, VITE_SSO_CLIENT_ID).
          </p>
        )}
        {devAdmin && (
          <form onSubmit={dev} className="border-line mt-5 border-t pt-4">
            <label className="text-ink-2 text-xs font-semibold" htmlFor="dev-name">
              Dev sign-in (laptop only)
            </label>
            <input
              id="dev-name"
              className="field mt-1"
              placeholder="your name"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <button type="submit" className="btn btn-accent mt-3 w-full" disabled={busy}>
              {busy ? 'Signing in…' : 'Sign in as dev'}
            </button>
          </form>
        )}
        {error && <p className="text-bad mt-3 text-sm">{error}</p>}
      </div>
    </div>
  )
}
