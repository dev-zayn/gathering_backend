import { api } from './api.js'

// Any OIDC provider: its addresses come from discovery, so nothing here names
// one. The console is a public client with PKCE; the API trades the access
// token for a cookie session of its own and the browser forgets the token.
const ISSUER = (import.meta.env.VITE_SSO_ISSUER ?? '').replace(/\/$/, '')
const CLIENT_ID = import.meta.env.VITE_SSO_CLIENT_ID ?? ''
const SCOPE = import.meta.env.VITE_SSO_SCOPE || 'openid profile email'

export const ssoConfigured = Boolean(ISSUER && CLIENT_ID)
export const devAdmin = import.meta.env.VITE_DEV_ADMIN === 'true'

const PENDING = 'ga.console.sso.pending'
const ID_TOKEN = 'ga.console.sso.id'

let discovery
async function discover() {
  if (discovery) return discovery
  const res = await fetch(`${ISSUER}/.well-known/openid-configuration`)
  if (!res.ok) throw new Error('The identity provider did not answer discovery.')
  discovery = await res.json()
  return discovery
}

function random(bytes = 32) {
  const a = new Uint8Array(bytes)
  crypto.getRandomValues(a)
  return base64url(a)
}

function base64url(bytes) {
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '')
}

async function challenge(verifier) {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  return base64url(new Uint8Array(digest))
}

export async function signIn(returnTo = '/') {
  const d = await discover()
  const verifier = random()
  const state = random(16)
  sessionStorage.setItem(PENDING, JSON.stringify({ verifier, state, returnTo }))
  const u = new URL(d.authorization_endpoint)
  u.searchParams.set('response_type', 'code')
  u.searchParams.set('client_id', CLIENT_ID)
  u.searchParams.set('redirect_uri', `${location.origin}/auth/callback`)
  u.searchParams.set('scope', SCOPE)
  u.searchParams.set('state', state)
  u.searchParams.set('code_challenge', await challenge(verifier))
  u.searchParams.set('code_challenge_method', 'S256')
  location.assign(u.toString())
}

// The callback: trade the code for tokens at the provider, then the access
// token for our cookie. Returns where to go next.
export async function finish() {
  const params = new URLSearchParams(location.search)
  const pending = JSON.parse(sessionStorage.getItem(PENDING) || 'null')
  sessionStorage.removeItem(PENDING)
  if (!pending || params.get('state') !== pending.state) throw new Error('The sign-in did not start here.')
  if (params.get('error')) throw new Error(params.get('error_description') || params.get('error'))
  const d = await discover()
  const res = await fetch(d.token_endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code: params.get('code') || '',
      redirect_uri: `${location.origin}/auth/callback`,
      client_id: CLIENT_ID,
      code_verifier: pending.verifier,
    }),
  })
  if (!res.ok) throw new Error('The identity provider refused the code.')
  const tokens = await res.json()
  if (tokens.id_token) localStorage.setItem(ID_TOKEN, tokens.id_token)
  await fetch(`${(import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '')}/v1/admin/session`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${tokens.access_token}` },
    credentials: 'include',
  }).then((r) => {
    if (!r.ok) throw new Error(r.status === 403 ? 'Your account has no role here.' : 'The API refused the token.')
  })
  return pending.returnTo || '/'
}

// A laptop only: the API mints an admin with no provider when DEBUG and
// DEV_ADMIN are on. The button shows only when the build says so.
export async function devSignIn(name) {
  await api.post('/v1/admin/session/dev', { name }, { auth: true })
}

export async function signOut() {
  try {
    await api.del('/v1/admin/session')
  } catch {
    /* already gone */
  }
  const id = localStorage.getItem(ID_TOKEN)
  localStorage.removeItem(ID_TOKEN)
  if (ssoConfigured && id) {
    try {
      const d = await discover()
      if (d.end_session_endpoint) {
        const u = new URL(d.end_session_endpoint)
        u.searchParams.set('id_token_hint', id)
        u.searchParams.set('post_logout_redirect_uri', `${location.origin}/`)
        location.assign(u.toString())
        return
      }
    } catch {
      /* fall through to a plain reload */
    }
  }
  location.assign('/')
}
