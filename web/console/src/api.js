import { explain } from './errors.js'

// Empty means the same origin (the dev proxy, or a deployment that fronts both).
const BASE = (import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '')
const TIMEOUT = 30_000

export class ApiError extends Error {
  constructor(message, status, data) {
    super(message)
    this.status = status
    this.data = data
  }
}

// A 401 on a signed-in call means the session is gone: the app hears this and
// starts a sign-in. `quiet` is for the boot check, which expects it.
export const SIGNED_OUT = 'ga:signed-out'

export async function send(method, path, { body, form, auth = true, quiet = false, signal } = {}) {
  const timeout = AbortSignal.timeout(TIMEOUT)
  const combined = signal ? AbortSignal.any([signal, timeout]) : timeout
  const headers = {}
  let payload
  if (form) payload = form
  else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  let res
  try {
    res = await fetch(`${BASE}${path}`, {
      method,
      headers,
      body: payload,
      credentials: auth ? 'include' : 'omit',
      signal: combined,
    })
  } catch (e) {
    if (timeout.aborted) throw new ApiError('The server took too long to answer.', 0)
    if (signal?.aborted) throw e
    throw new ApiError('Could not reach the server.', 0)
  }
  if (res.status === 401 && auth && !quiet) window.dispatchEvent(new Event(SIGNED_OUT))
  if (res.status === 204) return null
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = null
  }
  if (!res.ok) throw new ApiError(explain(data, res.status), res.status, data)
  return data
}

export const api = {
  get: (path, opts) => send('GET', path, opts),
  post: (path, body, opts) => send('POST', path, { ...opts, body }),
  patch: (path, body, opts) => send('PATCH', path, { ...opts, body }),
  put: (path, body, opts) => send('PUT', path, { ...opts, body }),
  del: (path, opts) => send('DELETE', path, opts),
  // A file, as the one `file` part of a multipart form.
  upload: (path, file, { method = 'PUT' } = {}) => {
    const form = new FormData()
    form.append('file', file)
    return send(method, path, { form })
  },
}
