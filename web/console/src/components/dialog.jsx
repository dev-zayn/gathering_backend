import { useEffect } from 'react'
import { X } from 'lucide-react'

// A panel over the page, for the one thing being decided. Escape and the
// backdrop both close it; a form inside submits with its own button.
export default function Dialog({ open, title, description, onClose, children, footer }) {
  useEffect(() => {
    if (!open) return undefined
    const onKey = (e) => {
      if (e.key === 'Escape') onClose?.()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null
  return (
    <div
      className="bg-ink/40 fixed inset-0 z-50 grid place-items-center p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose?.()
      }}
    >
      <div role="dialog" aria-modal="true" className="card w-full max-w-lg p-5 shadow-xl">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-lg font-extrabold">{title}</h2>
            {description && <p className="text-ink-2 mt-1 text-sm">{description}</p>}
          </div>
          <button type="button" aria-label="Close" className="text-ink-3 hover:text-ink shrink-0" onClick={onClose}>
            <X className="size-4" />
          </button>
        </div>
        <div className="mt-4">{children}</div>
        {footer && <div className="mt-5 flex justify-end gap-2">{footer}</div>}
      </div>
    </div>
  )
}
