// The other hosts the docs point to: the console where studios register and
// ship their games, the site, and the app in the two stores.
const trim = (url) => url.replace(/\/$/, '')

export const consoleUrl = trim(import.meta.env.VITE_CONSOLE_URL || 'https://console.ga3da.fun')
export const siteUrl = trim(import.meta.env.VITE_SITE_URL || 'https://ga3da.fun')
export const playStoreUrl = 'https://play.google.com/store/apps/details?id=app.gathering.jamaa'
export const appStoreUrl = 'https://apps.apple.com/us/app/ga3da-party-games/id6837319887'

export const ELSEWHERE = [
  { href: consoleUrl, label: 'Console' },
  { href: siteUrl, label: 'ga3da.fun' },
  { href: playStoreUrl, label: 'Google Play' },
  { href: appStoreUrl, label: 'App Store' },
]
