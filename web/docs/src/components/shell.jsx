import { createContext, useContext, useEffect, useState } from 'react'
import { Link, NavLink, useNavigate } from 'react-router-dom'
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  ChevronRightIcon,
  MenuIcon,
  SearchIcon,
  XIcon,
} from 'lucide-react'

import { Search } from '@/components/search'
import { GROUPS, HOME, PAGES, SECTION } from '@/pages.js'
import { cn } from '@/lib/utils'

// The frame around every page. The rail is the console's: a ceramic panel
// with a tile per page, the open one lit orange and its sections hanging
// under it on a track. Above the prose a frosted bar with where you are and
// the search; along its top edge, how far down the page you are.

// The open page and its sections, for the pieces that draw them.
export const PageContext = createContext({ page: null, sections: [], active: null })

export function Shell({ page, sections, children }) {
  const [search, setSearch] = useState(false)
  const [menu, setMenu] = useState(false)
  const active = useActiveSection(sections)
  const go = useNavigate()

  useEffect(() => setMenu(false), [page])

  // ← and → page through, / opens the search; never while typing.
  useEffect(() => {
    const key = (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const t = e.target
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
      if (e.key === '/') {
        e.preventDefault()
        setSearch(true)
      }
      if (!page) return
      if (e.key === 'ArrowRight' && PAGES[page.n + 1]) go(PAGES[page.n + 1].href)
      if (e.key === 'ArrowLeft' && PAGES[page.n - 1]) go(PAGES[page.n - 1].href)
    }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [page, go])

  return (
    <PageContext.Provider value={{ page, sections, active }}>
      <div className="bg-ground-lit min-h-svh">
        <a
          href="#main"
          className="sr-only bg-accent text-white focus:not-sr-only focus:fixed focus:start-4 focus:top-4 focus:z-60 focus:rounded-md focus:px-4 focus:py-2"
        >
          Skip to content
        </a>

        <Rail className="fixed inset-y-0 start-0 z-20 hidden w-[19rem] lg:flex" />

        {menu && (
          <div
            className="skeu-backdrop animate-fade fixed inset-0 z-40 lg:hidden"
            onPointerDown={(e) => e.target === e.currentTarget && setMenu(false)}
          >
            <Rail
              className="absolute inset-y-0 start-0 flex w-[19rem] animate-slide-in"
              onClose={() => setMenu(false)}
            />
          </div>
        )}

        <div className="relative z-10 lg:ps-[19rem]">
          <header className="sticky top-0 z-30 px-4 pt-3 sm:px-6">
            <div className="skeu-rail relative flex h-14 items-center gap-2 overflow-hidden rounded-xl ps-3 pe-2.5">
              <Progress />
              <button
                type="button"
                onClick={() => setMenu(true)}
                className="skeu-btn skeu-btn-ghost inline-flex size-9 shrink-0 items-center justify-center rounded-md lg:hidden"
                aria-label="Open the pages"
              >
                <MenuIcon className="size-4" />
              </button>
              <Where page={page} />
              <div className="ms-auto flex min-w-0 flex-1 items-center justify-end gap-2 sm:flex-none">
                <button
                  type="button"
                  onClick={() => setSearch(true)}
                  className="skeu-input flex h-9 w-full items-center gap-2 rounded-md px-3 text-[13px] text-ink-3 sm:w-60"
                >
                  <SearchIcon className="size-4" aria-hidden="true" />
                  <span className="flex-1 truncate text-start">Search…</span>
                  <span className="kbd hidden sm:inline-flex">/</span>
                </button>
                {page && <Paging page={page} />}
                {SECTION === 'developer' && (
                  <Link
                    to="/quickstart"
                    className="skeu-btn skeu-btn-primary hidden h-9 shrink-0 items-center gap-1.5 rounded-md px-3.5 text-[13px] font-semibold md:inline-flex"
                  >
                    Start building
                    <ArrowRightIcon className="size-3.5" aria-hidden="true" />
                  </Link>
                )}
              </div>
            </div>
          </header>

          <div className="w-full px-4 pb-16 sm:px-6">
            <main id="main" className="min-w-0">
              <article key={page?.href ?? '404'} className="prose stagger pt-8">
                {children}
              </article>
              {page && <PrevNext page={page} />}
            </main>
            <footer className="mt-12 flex flex-wrap items-center justify-between gap-3 border-t border-line pt-6 text-xs text-ink-3">
              <span>© {new Date().getFullYear()} Gathering. All rights reserved.</span>
              <span className="inline-flex items-center gap-2">
                <span className="kbd">←</span>
                <span className="kbd">→</span>
                page through
                <span className="kbd ms-2">/</span>
                search
              </span>
            </footer>
          </div>
        </div>

        <Search open={search} onOpenChange={setSearch} />
      </div>
    </PageContext.Provider>
  )
}

// Where you are, as the bar's left half. At the top of a page it is the
// group and the page. Once the page's own header has scrolled away the bar
// stands in for it: the page's tile, its name, and the section on screen.
function Where({ page }) {
  const { sections, active } = useContext(PageContext)
  const deep = useScrolledPast(180)
  if (!page) return null
  const section = deep ? sections.find((s) => s.id === active) : null
  return (
    <nav
      aria-label="Where you are"
      className="hidden min-w-0 items-center gap-1.5 text-[13px] sm:flex"
    >
      {deep ? (
        <span className="skeu-tile animate-fade inline-flex size-6 shrink-0 items-center justify-center rounded-md">
          <page.Icon className="size-3.5" aria-hidden="true" />
        </span>
      ) : (
        <span className="animate-fade text-ink-3">{page.group}</span>
      )}
      <ChevronRightIcon className="size-3.5 shrink-0 text-ink-3" aria-hidden="true" />
      <span className={cn('truncate font-semibold', section ? 'text-ink-2' : 'text-ink')}>
        {page.title}
      </span>
      {section && (
        <>
          <ChevronRightIcon className="size-3.5 shrink-0 text-ink-3" aria-hidden="true" />
          <span key={section.id} className="animate-fade truncate font-semibold text-ink">
            {section.title}
          </span>
        </>
      )}
    </nav>
  )
}

// True once the page has scrolled past the given height.
function useScrolledPast(px) {
  const [past, setPast] = useState(false)
  useEffect(() => {
    const on = () => setPast(window.scrollY > px)
    on()
    window.addEventListener('scroll', on, { passive: true })
    return () => window.removeEventListener('scroll', on)
  }, [px])
  return past
}

// The page before and after, as one pair of keys. The same as ← and →.
function Paging({ page }) {
  const prev = PAGES[page.n - 1]
  const next = PAGES[page.n + 1]
  return (
    <div className="skeu-well-soft hidden shrink-0 items-center gap-0.5 rounded-md p-0.5 md:flex">
      <PageKey to={prev?.href} label={prev && `Previous: ${prev.title}`} Icon={ArrowLeftIcon} />
      <PageKey to={next?.href} label={next && `Next: ${next.title}`} Icon={ArrowRightIcon} />
    </div>
  )
}

function PageKey({ to, label, Icon }) {
  const cls = 'inline-flex size-8 items-center justify-center rounded-[5px]'
  if (!to) {
    return (
      <span className={cn(cls, 'text-ink-3/50')} aria-hidden="true">
        <Icon className="size-4" />
      </span>
    )
  }
  return (
    <Link
      to={to}
      className={cn(cls, 'skeu-btn skeu-btn-ghost text-ink-2 hover:text-ink')}
      aria-label={label}
      title={label}
    >
      <Icon className="size-4" />
    </Link>
  )
}

// How far down the page you are, along the bar's top edge.
function Progress() {
  const [p, setP] = useState(0)
  useEffect(() => {
    const on = () => {
      const h = document.documentElement
      const max = h.scrollHeight - h.clientHeight
      setP(max > 0 ? Math.min(1, h.scrollTop / max) : 0)
    }
    on()
    window.addEventListener('scroll', on, { passive: true })
    window.addEventListener('resize', on)
    return () => {
      window.removeEventListener('scroll', on)
      window.removeEventListener('resize', on)
    }
  }, [])
  return (
    <span aria-hidden="true" className="absolute inset-x-0 top-0 h-0.5 bg-black/5">
      <span
        className="skeu-fill block h-full transition-[width] duration-150"
        style={{ width: `${p * 100}%` }}
      />
    </span>
  )
}

function Rail({ className, onClose }) {
  const { page, sections, active } = useContext(PageContext)
  return (
    <nav aria-label="Pages" className={cn('flex-col p-3.5', className)}>
      <div className="skeu-panel flex h-full flex-col overflow-hidden rounded-2xl">
        <div className="flex items-center gap-3 px-4 pt-4 pb-2">
          <Link to={HOME} className="flex items-center gap-3">
            <span className="skeu-chip-solid inline-flex size-8 items-center justify-center rounded-lg text-sm font-extrabold">
              ج
            </span>
            <span className="text-sm font-extrabold text-ink">Gathering</span>
            <span className="text-label border-s border-line ps-3">
              {SECTION === 'app' ? 'The app' : 'Developers'}
            </span>
          </Link>
          {onClose && (
            <button
              type="button"
              onClick={onClose}
              className="skeu-btn skeu-btn-ghost ms-auto inline-flex size-8 items-center justify-center rounded-md"
              aria-label="Close"
            >
              <XIcon className="size-4" />
            </button>
          )}
        </div>

        <div className="flex-1 overflow-y-auto px-3 pb-3">
          {GROUPS.map((g) => (
            <div key={g.title} className="mt-4">
              <div className="text-label px-2 pb-1.5">{g.title}</div>
              <ul className="flex flex-col gap-0.5">
                {g.pages.map((p) => {
                  const current = page?.href === p.href
                  return (
                    <li key={p.href}>
                      <NavLink
                        to={p.href}
                        end
                        className={cn(
                          'skeu-navkey flex h-9 items-center gap-2.5 rounded-lg px-1.5 text-[13px] font-semibold',
                          current && 'skeu-navkey-current',
                        )}
                      >
                        <span
                          className={cn(
                            'inline-flex size-6 shrink-0 items-center justify-center rounded-md transition-[box-shadow,color] duration-200',
                            current ? 'skeu-tile' : 'skeu-tile-ceramic text-ink-3',
                          )}
                        >
                          <p.Icon className="size-3.5" aria-hidden="true" />
                        </span>
                        <span className="truncate">{p.title}</span>
                      </NavLink>
                      {current && sections.length > 0 && (
                        <ol className="stagger relative my-1.5 ms-4 flex flex-col before:absolute before:inset-y-1 before:start-0 before:w-px before:bg-line-2">
                          {sections.map((s) => {
                            const here = active === s.id
                            return (
                              <li key={s.id} className="relative">
                                <Link
                                  to={`#${s.id}`}
                                  className={cn(
                                    'block truncate rounded-md py-1 ps-4 pe-2 text-xs transition',
                                    here
                                      ? 'font-semibold text-accent-ink'
                                      : 'text-ink-2 hover:text-ink',
                                  )}
                                >
                                  <span
                                    aria-hidden="true"
                                    className={cn(
                                      'absolute start-[-3px] top-1/2 size-[7px] -translate-y-1/2 rounded-full transition',
                                      here ? 'skeu-chip-solid' : 'bg-line-2',
                                    )}
                                  />
                                  {s.title}
                                </Link>
                              </li>
                            )
                          })}
                        </ol>
                      )}
                    </li>
                  )
                })}
              </ul>
            </div>
          ))}
        </div>
      </div>
    </nav>
  )
}

// Which section is on screen, for the rail and the header to mark.
function useActiveSection(sections) {
  const [active, setActive] = useState(null)
  useEffect(() => {
    const heads = sections.map((s) => document.getElementById(s.id)).filter(Boolean)
    if (heads.length === 0) return undefined
    const seen = new Map()
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) seen.set(e.target.id, e.isIntersecting)
        const first = heads.find((h) => seen.get(h.id))
        if (first) setActive(first.id)
      },
      { rootMargin: '-80px 0px -60% 0px' },
    )
    heads.forEach((h) => io.observe(h))
    return () => io.disconnect()
  }, [sections])
  return active
}

function PrevNext({ page }) {
  const prev = PAGES[page.n - 1]
  const next = PAGES[page.n + 1]
  if (!prev && !next) return null
  return (
    <nav aria-label="Previous and next" className="not-prose mt-14 grid gap-3 sm:grid-cols-2">
      {prev ? (
        <Link
          to={prev.href}
          className="skeu-panel skeu-panel-interactive flex items-center gap-3 rounded-xl p-4"
        >
          <ArrowLeftIcon className="size-4 shrink-0 text-ink-3" aria-hidden="true" />
          <span className="min-w-0">
            <span className="text-label block">Previous</span>
            <span className="block truncate text-sm font-bold text-ink">{prev.title}</span>
          </span>
        </Link>
      ) : (
        <span />
      )}
      {next && (
        <Link
          to={next.href}
          className="skeu-panel skeu-panel-interactive flex items-center justify-end gap-3 rounded-xl p-4 text-end"
        >
          <span className="min-w-0">
            <span className="text-label block">Next</span>
            <span className="block truncate text-sm font-bold text-ink">{next.title}</span>
          </span>
          <span className="skeu-tile inline-flex size-8 shrink-0 items-center justify-center rounded-md">
            <ArrowRightIcon className="size-4" aria-hidden="true" />
          </span>
        </Link>
      )}
    </nav>
  )
}
