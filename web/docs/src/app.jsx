import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { MDXProvider } from '@mdx-js/react'

import { Shell } from '@/components/shell'
import * as mdx from '@/components/mdx'
import { NotFound } from '@/components/blocks'
import { pageAt } from '@/pages.js'

// One page at a time: the route names it, its module is loaded, and its
// sections feed the sidebar and the rail before the prose is drawn.
export function App() {
  const { pathname, hash } = useLocation()
  const page = pageAt(pathname)
  const [loaded, setLoaded] = useState(null)

  useEffect(() => {
    if (!page) return undefined
    let live = true
    page.load().then((mod) => {
      if (live) setLoaded({ href: page.href, Content: mod.default, sections: mod.sections ?? [] })
    })
    return () => {
      live = false
    }
  }, [page])

  // A new page starts at the top; a hash goes to its section once it exists.
  useEffect(() => {
    if (!loaded || loaded.href !== page?.href) return
    if (hash) {
      const el = document.getElementById(hash.slice(1))
      if (el) {
        el.scrollIntoView({ block: 'start' })
        return
      }
    }
    window.scrollTo(0, 0)
  }, [loaded, page, hash])

  useEffect(() => {
    document.title = page
      ? `${page.title} · Gathering developers`
      : 'Not found · Gathering developers'
  }, [page])

  const current = loaded && loaded.href === page?.href ? loaded : null

  return (
    <Shell page={page} sections={current?.sections ?? []}>
      <MDXProvider components={mdx}>
        {!page ? <NotFound /> : current ? <current.Content /> : <Loading />}
      </MDXProvider>
    </Shell>
  )
}

function Loading() {
  return (
    <div className="animate-fade flex flex-col gap-4 pt-4">
      <div className="skeu-skeleton h-9 w-2/3 rounded-md" />
      <div className="skeu-skeleton h-5 w-full rounded-md" />
      <div className="skeu-skeleton h-5 w-5/6 rounded-md" />
    </div>
  )
}
