// The other hosts the console points to: the developer docs (the publisher's
// guide is their "Getting listed" page) and the site with the app's stores.
const trim = (url) => url.replace(/\/$/, '')

export const docsUrl = trim(import.meta.env.VITE_DOCS_URL || 'https://docs.ga3da.fun')
export const listingGuide = `${docsUrl}/listing`
export const siteUrl = trim(import.meta.env.VITE_SITE_URL || 'https://ga3da.fun')
