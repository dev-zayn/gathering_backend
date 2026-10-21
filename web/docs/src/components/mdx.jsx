import { useContext } from 'react'
import { Link } from 'react-router-dom'
import { ArrowRightIcon, InfoIcon, LinkIcon } from 'lucide-react'

import { PAGES, known } from '@/pages.js'
import { Code, CodeGroup, Pre, Tag } from '@/components/code'
import { PageContext } from '@/components/shell'
import { cn } from '@/lib/utils'

// What a page is written with. Markdown's own elements come through the
// provider; the rest are named in the page.

export { CodeGroup }
export const pre = Pre
export const code = Code

// A link inside this site stays inside it. One to the other section is a
// full page load, and one to the world opens the world.
export function a({ href = '', ...props }) {
  if (href.startsWith('#') || known(href)) return <Link to={href} {...props} />
  if (href.startsWith('/')) return <a href={href} {...props} />
  return <a href={href} target="_blank" rel="noopener noreferrer" {...props} />
}

// The page's title wears the page's own tile, says where it sits in its
// group, and lists its sections as keys, so a reader sees the shape of the
// page before the first paragraph.
function Title({ children }) {
  const { page, sections, active } = useContext(PageContext)
  return (
    <header className="not-prose mb-8">
      <div className="flex items-start gap-4">
        {page?.Icon && (
          <span className="skeu-tile mt-1 inline-flex size-12 shrink-0 items-center justify-center rounded-xl">
            <page.Icon className="size-6" aria-hidden="true" />
          </span>
        )}
        <div className="min-w-0">
          {page && (
            <div className="text-label mb-1.5 flex items-center gap-2">
              <span>{page.group}</span>
              <span className="text-line-2">·</span>
              <span className="tabular-nums">
                {page.n + 1} of {PAGES.length}
              </span>
            </div>
          )}
          <h1 className="text-[34px] leading-[1.15] font-extrabold text-ink">{children}</h1>
        </div>
      </div>
      {sections.length > 1 && (
        <ul className="mt-5 flex flex-wrap gap-1.5">
          {sections.map((s) => (
            <li key={s.id}>
              <Link
                to={`#${s.id}`}
                className={cn(
                  'skeu-tab inline-flex h-7 items-center rounded-full px-3 text-xs font-semibold text-ink-2',
                  active === s.id && 'skeu-tab-soft-active',
                )}
              >
                {s.title}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </header>
  )
}

export const h1 = Title

// An h2 can carry the route it documents, and a link to itself for the
// reader who wants to send it on.
export function h2({ children, id, tag, label, anchor = true, ...props }) {
  return (
    <>
      {(tag || label) && (
        <div className="not-prose skeu-well-soft mt-11 mb-3 flex w-fit items-center gap-2 rounded-md py-1 ps-1.5 pe-3">
          {tag && <Tag>{tag}</Tag>}
          {label && <span className="text-id text-[13px] font-medium text-ink">{label}</span>}
        </div>
      )}
      <h2 id={id} className={cn('group', (tag || label) && 'mt-2!')} {...props}>
        {anchor && id ? (
          <Link
            to={`#${id}`}
            className="not-prose inline-flex items-center gap-2 text-inherit no-underline!"
          >
            {children}
            <LinkIcon
              className="size-4 text-ink-3 opacity-0 transition group-hover:opacity-100"
              aria-hidden="true"
            />
          </Link>
        ) : (
          children
        )}
      </h2>
    </>
  )
}

export function Note({ children }) {
  return (
    <div className="not-prose skeu-alert skeu-alert-info my-6 flex gap-3 rounded-lg px-4 py-3.5 text-sm leading-relaxed text-ink">
      <InfoIcon className="mt-0.5 size-4 shrink-0 text-info" aria-hidden="true" />
      <div className="min-w-0 [&_a]:font-medium [&_a]:text-accent-ink [&_a]:underline [&_code]:text-id [&_code]:text-[0.9em] [&_p]:my-0">
        {children}
      </div>
    </div>
  )
}

export function Row({ children }) {
  return (
    <div className="grid grid-cols-1 items-start gap-x-12 gap-y-8 xl:grid-cols-2">{children}</div>
  )
}

export function Col({ children, sticky = false }) {
  return (
    <div className={cn('min-w-0 *:first:mt-0 *:last:mb-0', sticky && 'xl:sticky xl:top-24')}>
      {children}
    </div>
  )
}

// One row per field: name and shape first, meaning underneath.
export function Properties({ children }) {
  return (
    <div className="not-prose skeu-well-soft my-6 overflow-hidden rounded-lg">
      <ul className="divide-y divide-line">{children}</ul>
    </div>
  )
}

export function Property({ name, type, required = false, children }) {
  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
        <span className="text-id text-[13px] font-semibold text-ink">{name}</span>
        {type && (
          <span className="skeu-chip-neutral text-id inline-flex h-5 items-center rounded-full px-2 text-[11px] text-ink-2">
            {type}
          </span>
        )}
        {required && <span className="text-label text-accent-ink">required</span>}
      </div>
      <div className="mt-1 text-[13.5px] leading-relaxed text-ink-2 [&_a]:font-medium [&_a]:text-accent-ink [&_a]:underline [&_code]:text-id [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-surface-2 [&_code]:px-1 [&_code]:text-[0.9em] [&_code]:text-ink [&_p]:my-0">
        {children}
      </div>
    </li>
  )
}

const looks = {
  primary: 'skeu-btn skeu-btn-primary h-10 rounded-md px-5 text-sm',
  outline: 'skeu-btn skeu-btn-secondary h-10 rounded-md px-5 text-sm text-ink',
  text: 'h-auto text-accent-ink hover:underline underline-offset-4 text-sm',
}

export function Button({ href, variant = 'primary', arrow, icon: Icon, children, className }) {
  const inner = (
    <>
      {Icon && <Icon className="size-4" aria-hidden="true" />}
      {children}
      {arrow === 'right' && <ArrowRightIcon className="size-4" aria-hidden="true" />}
    </>
  )
  const cls = cn(
    'not-prose inline-flex items-center justify-center gap-1.5 font-semibold whitespace-nowrap',
    looks[variant],
    className,
  )
  return known(href ?? '') ? (
    <Link to={href} className={cls}>
      {inner}
    </Link>
  ) : href?.startsWith('/') ? (
    <a href={href} className={cls}>
      {inner}
    </a>
  ) : (
    <a href={href} className={cls} target="_blank" rel="noopener noreferrer">
      {inner}
    </a>
  )
}
