import fs from 'node:fs'
import path from 'node:path'

import { inSection, sectionOf } from '../src/section.js'

// The page list, cut to the section being built, at build time. The other
// section's titles are not in this bundle either: a developer's browser holds
// nothing of the app's pages, not even its table of contents.
export function listPages() {
  const section = sectionOf(process.env.VITE_SECTION)
  const all = JSON.parse(fs.readFileSync(path.resolve('pages.json'), 'utf8'))
  return all.filter((p) => inSection(p, section))
}

export function pagesList() {
  const id = 'virtual:pages'
  return {
    name: 'pages-list',
    resolveId(source) {
      return source === id ? '\0' + id : null
    },
    load(source) {
      if (source !== '\0' + id) return null
      return `export default ${JSON.stringify(listPages())}`
    },
  }
}
