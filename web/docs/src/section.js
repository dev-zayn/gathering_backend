// Two sites come out of one codebase. The developer site is public; the
// app section (the mobile team's pages) is built on its own, served under
// /app/ behind a password, and never appears in the public rail or search.
export const APP_GROUP = 'The app'

export function sectionOf(env) {
  return env === 'app' ? 'app' : 'developer'
}

// Whether a page belongs to the section being built.
export function inSection(page, section) {
  return (page.group === APP_GROUP) === (section === 'app')
}

// Where a page's Markdown copy sits, under its section's base (mdx/llms.mjs
// writes them): `/` is index.md, `/rooms` rooms.md, and `/app/signin`, under
// /app/, signin.md.
export function markdownFile(page, section) {
  const rel = section === 'app' ? page.href.replace(/^\/app\/?/, '') : page.href.slice(1)
  return (rel || 'index') + '.md'
}
