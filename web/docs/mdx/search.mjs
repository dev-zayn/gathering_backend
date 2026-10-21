import fs from 'node:fs'
import path from 'node:path'
import { slugifyWithCounter } from '@sindresorhus/slugify'

import { listPages } from './pages.mjs'

// The search index, built from the pages at build time: one entry per
// section, with its words stripped of markup. Small enough to ship whole,
// so searching is a filter in the browser and needs no service.
export function searchIndex() {
  const id = 'virtual:search-index'
  return {
    name: 'search-index',
    resolveId(source) {
      return source === id ? '\0' + id : null
    },
    load(source) {
      if (source !== '\0' + id) return null
      const pages = listPages()
      const entries = []
      for (const page of pages) {
        const src = fs.readFileSync(path.resolve('src/content', `${page.file}.mdx`), 'utf8')
        const slugify = slugifyWithCounter()
        let section = { title: page.title, id: '', text: [] }
        const flush = () => {
          // A page's opening may be all components (the home page is); the
          // page's description stands in for it.
          const text = clean(section.text.join('\n')) || (section.id === '' ? page.description : '')
          if (text || section.id === '')
            entries.push({
              href: page.href,
              page: page.title,
              title: section.title,
              id: section.id,
              text,
            })
        }
        for (const line of src.split('\n')) {
          const h2 = line.match(/^## (.+?)(?:\s*\{\{.*\}\})?\s*$/)
          if (h2) {
            flush()
            section = { title: h2[1].trim(), id: slugify(h2[1].trim()), text: [] }
          } else {
            section.text.push(line)
          }
        }
        flush()
      }
      return `export default ${JSON.stringify(entries)}`
    },
  }
}

function clean(s) {
  const text = s
    .replace(/```[\s\S]*?```/g, ' ')
    // An import or export runs to its blank line, however many lines it takes.
    .replace(/^(import|export)\b[\s\S]*?^\s*$/gm, ' ')
    // The page's own title and the rules between sections are not words about it.
    .replace(/^# .*$/gm, ' ')
    .replace(/^---\s*$/gm, ' ')
    .replace(/<[^>]+>/g, ' ')
    .replace(/\{\{[^}]*\}\}/g, ' ')
    .replace(/\{[^}]*\}/g, ' ')
    .replace(/[#*_`>[\]()]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 220)
  // Punctuation alone is not a description.
  return /[\p{L}\p{N}]/u.test(text) ? text : ''
}
