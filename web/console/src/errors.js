// One sentence for every answer the API gives, so a screen never shows a code.
const sentences = {
  unauthorized: 'Sign in again.',
  forbidden: 'Your account cannot do that.',
  no_role: 'Your account has no role here. Ask for one.',
  bad_origin: 'This page is not allowed to use your session.',
  not_found: 'Not found.',
  invalid_fields: 'Some fields need attention.',
  too_many_requests: 'Too many requests. Wait a moment.',
  identity_unavailable: 'The identity provider is not answering.',
  internal_error: 'Something went wrong on our side.',
  not_supported: 'Not available yet.',
}

export function explain(data, status) {
  const code = data?.error
  if (code === 'invalid_fields' && data.fields) {
    return Object.entries(data.fields)
      .map(([k, v]) => `${k}: ${v}`)
      .join(', ')
  }
  if (code === 'incomplete' && data.missing) return `Missing: ${data.missing.join(', ')}`
  if (code === 'too_many_requests' && data.retry_after) return `Too many requests. Try again in ${data.retry_after}s.`
  if (code && sentences[code]) return sentences[code]
  if (code) return code.replaceAll('_', ' ')
  if (status === 401) return sentences.unauthorized
  if (status === 403) return sentences.forbidden
  if (status === 404) return sentences.not_found
  if (status >= 500) return sentences.internal_error
  return `Request failed (${status})`
}
