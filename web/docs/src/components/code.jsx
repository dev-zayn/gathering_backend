import { Children, createContext, isValidElement, useContext, useEffect, useState } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'

import { cn } from '@/lib/utils'

// The brand's code block: graphite, a top bar naming the code — its title, or
// a tab per language when there is more than one — with the dark copy key on
// its end edge, the route under it when it is a request, and the code itself
// pinned left-to-right so an Arabic page never mirrors it.

const names = {
  js: 'JavaScript',
  javascript: 'JavaScript',
  ts: 'TypeScript',
  typescript: 'TypeScript',
  go: 'Go',
  php: 'PHP',
  python: 'Python',
  py: 'Python',
  ruby: 'Ruby',
  bash: 'Shell',
  sh: 'Shell',
  json: 'JSON',
  http: 'HTTP',
  html: 'HTML',
  css: 'CSS',
  text: 'Text',
  txt: 'Text',
}

const Grouped = createContext(false)

function titleOf(props) {
  return props.title ?? names[props.language] ?? 'Code'
}

const tagLook = {
  GET: 'skeu-chip-solid',
  POST: 'bg-info text-white',
  PUT: 'bg-warn text-white',
  PATCH: 'bg-warn text-white',
  DELETE: 'skeu-chip-error',
}

export function Tag({ children }) {
  return (
    <span
      className={cn(
        'inline-flex h-5 items-center rounded-full px-2 text-[10px] font-bold tracking-wide',
        tagLook[children] ?? 'skeu-chip-neutral text-ink-2',
      )}
    >
      {children}
    </span>
  )
}

function Copy({ code }) {
  const [done, setDone] = useState(false)
  useEffect(() => {
    if (!done) return undefined
    const t = setTimeout(() => setDone(false), 1400)
    return () => clearTimeout(t)
  }, [done])
  return (
    <button
      type="button"
      onClick={() => navigator.clipboard.writeText(code).then(() => setDone(true))}
      aria-live="polite"
      className="skeu-btn skeu-btn-dark inline-flex h-7 shrink-0 items-center gap-1.5 rounded-[var(--skeu-radius-sm)] px-2.5 text-[11px] font-semibold"
    >
      {done ? (
        <span className="inline-flex items-center gap-1.5 text-good">
          <CheckIcon className="size-3.5" aria-hidden="true" />
          Copied
        </span>
      ) : (
        <>
          <CopyIcon className="size-3.5" aria-hidden="true" />
          Copy
        </>
      )}
    </button>
  )
}

function Panel({ child, tag, label }) {
  const props = isValidElement(child) ? child.props : {}
  tag = props.tag ?? tag
  label = props.label ?? label
  return (
    <div className="group">
      {(tag || label) && (
        <div className="flex h-9 items-center gap-2.5 border-b border-white/8 px-4">
          {tag && <Tag>{tag}</Tag>}
          {label && <span className="text-id text-xs text-white/70">{label}</span>}
        </div>
      )}
      <pre dir="ltr" className="overflow-x-auto p-4 text-start">
        {child}
      </pre>
    </div>
  )
}

export function CodeGroup({ children, title, tag, label }) {
  const panels = Children.toArray(children)
  const [index, setIndex] = useState(0)
  const tabs = panels.length > 1
  const shown = panels[tabs ? index : 0]
  const code = isValidElement(shown) ? shown.props.code : undefined
  return (
    <Grouped.Provider value={true}>
      <div className="code-panel not-prose skeu-dark my-6 overflow-hidden rounded-xl">
        <div className="flex min-h-11 flex-wrap items-center gap-x-4 border-b border-white/10 bg-white/4 ps-4 pe-2">
          {title ? (
            <h3 className="py-3 text-xs font-bold text-white">{title}</h3>
          ) : (
            !tabs && (
              <span className="py-3 font-mono text-[10px] tracking-[0.16em] text-white/60 uppercase">
                {titleOf(isValidElement(shown) ? shown.props : {})}
              </span>
            )
          )}
          {tabs && (
            <div role="tablist" className="-mb-px flex gap-4 text-xs font-semibold">
              {panels.map((p, i) => (
                <button
                  key={i}
                  type="button"
                  role="tab"
                  aria-selected={i === index}
                  onClick={() => setIndex(i)}
                  className={cn(
                    'border-b-2 py-3 transition',
                    i === index
                      ? 'border-accent text-white'
                      : 'border-transparent text-white/60 hover:text-white/85',
                  )}
                >
                  {titleOf(isValidElement(p) ? p.props : {})}
                </button>
              ))}
            </div>
          )}
          {code && (
            <span className="ms-auto">
              <Copy code={code} />
            </span>
          )}
        </div>
        <div key={tabs ? index : 0} className="animate-fade">
          <Panel child={shown} tag={tag} label={label} />
        </div>
      </div>
    </Grouped.Provider>
  )
}

// A fence outside a group is a group of one. Inside one it is the panel.
export function Pre({ children, ...props }) {
  const grouped = useContext(Grouped)
  if (grouped) return children
  return <CodeGroup {...props}>{children}</CodeGroup>
}

// The highlighter's HTML, or a plain inline code in a sentence.
export function Code({ children, ...props }) {
  const grouped = useContext(Grouped)
  if (grouped && typeof children === 'string') {
    return <code {...props} dangerouslySetInnerHTML={{ __html: children }} />
  }
  return <code {...props}>{children}</code>
}
