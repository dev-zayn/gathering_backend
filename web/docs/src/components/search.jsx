import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { CornerDownLeftIcon, FileTextIcon, SearchIcon } from 'lucide-react'

import index from 'virtual:search-index'
import { cn } from '@/lib/utils'

// ⌘K. A filter over every section of every page, built at build time, so
// there is nothing to call and nothing to be down.
export function Search({ open, onOpenChange }) {
  const go = useNavigate()
  const [q, setQ] = useState('')
  const [cursor, setCursor] = useState(0)
  const input = useRef(null)

  useEffect(() => {
    const key = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        onOpenChange(!open)
      }
      if (e.key === 'Escape' && open) onOpenChange(false)
    }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [open, onOpenChange])

  useEffect(() => {
    if (!open) return undefined
    setQ('')
    setCursor(0)
    const t = setTimeout(() => input.current?.focus(), 30)
    const { overflow } = document.body.style
    document.body.style.overflow = 'hidden'
    return () => {
      clearTimeout(t)
      document.body.style.overflow = overflow
    }
  }, [open])

  const hits = useMemo(() => {
    const term = q.trim().toLowerCase()
    if (!term) return index.filter((e) => e.id === '').slice(0, 8)
    const words = term.split(/\s+/)
    return index
      .map((e) => {
        const hay = `${e.page} ${e.title} ${e.text}`.toLowerCase()
        if (!words.every((w) => hay.includes(w))) return null
        const score =
          (e.title.toLowerCase().includes(term) ? 3 : 0) +
          (e.page.toLowerCase().includes(term) ? 2 : 0)
        return { ...e, score }
      })
      .filter(Boolean)
      .sort((a, b) => b.score - a.score)
      .slice(0, 10)
  }, [q])

  function pick(hit) {
    onOpenChange(false)
    go(hit.id ? `${hit.href}#${hit.id}` : hit.href)
  }

  if (!open) return null
  return createPortal(
    <div
      className="skeu-backdrop animate-fade fixed inset-0 z-50 flex items-start justify-center p-4 pt-[12vh]"
      onPointerDown={(e) => e.target === e.currentTarget && onOpenChange(false)}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Search"
        className="skeu-dialog animate-skeu-dialog flex w-full max-w-xl flex-col overflow-hidden rounded-2xl"
      >
        <div className="flex items-center gap-3 border-b border-line px-4">
          <SearchIcon className="size-4 shrink-0 text-ink-3" aria-hidden="true" />
          <input
            ref={input}
            value={q}
            onChange={(e) => {
              setQ(e.target.value)
              setCursor(0)
            }}
            onKeyDown={(e) => {
              if (e.key === 'ArrowDown') {
                e.preventDefault()
                setCursor((c) => Math.min(c + 1, hits.length - 1))
              } else if (e.key === 'ArrowUp') {
                e.preventDefault()
                setCursor((c) => Math.max(c - 1, 0))
              } else if (e.key === 'Enter' && hits[cursor]) {
                pick(hits[cursor])
              }
            }}
            placeholder="Search the docs…"
            aria-label="Search the docs"
            className="h-13 flex-1 bg-transparent text-[15px] text-ink outline-none placeholder:text-ink-3"
          />
          <span className="kbd">esc</span>
        </div>
        <ul className="max-h-[50vh] overflow-y-auto p-2">
          {hits.length === 0 && (
            <li className="px-3 py-8 text-center text-sm text-ink-3">Nothing matches.</li>
          )}
          {hits.map((h, i) => (
            <li key={`${h.href}#${h.id}`}>
              <button
                type="button"
                onMouseEnter={() => setCursor(i)}
                onClick={() => pick(h)}
                className={cn(
                  'flex w-full items-start gap-3 rounded-lg px-3 py-2.5 text-start',
                  i === cursor ? 'skeu-well-accent' : 'hover:bg-black/4',
                )}
              >
                <FileTextIcon className="mt-0.5 size-4 shrink-0 text-ink-3" aria-hidden="true" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-semibold text-ink">
                    {h.id ? h.title : h.page}
                    {h.id && <span className="font-normal text-ink-3"> · {h.page}</span>}
                  </span>
                  {h.text && (
                    <span className="mt-0.5 block truncate text-xs text-ink-2">{h.text}</span>
                  )}
                </span>
                {i === cursor && (
                  <CornerDownLeftIcon
                    className="mt-1 size-3.5 shrink-0 text-ink-3"
                    aria-hidden="true"
                  />
                )}
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>,
    document.body,
  )
}
