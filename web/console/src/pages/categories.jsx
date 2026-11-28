import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Note } from '../components/game-bits.jsx'
import { useCategories } from '../lib/games.js'
import { useMe } from '../lib/me.js'

// The store's categories (docs/CATEGORIES.md): the groups the app's Games tab
// shows, in this order, each named in both of the app's languages. A game
// carries one; Other is where a new game waits and where a deleted
// category's games go, so it cannot be deleted.
export default function Categories() {
  const me = useMe()
  const canWrite = me?.roles?.includes('admin')
  const list = useCategories()
  const rows = list.data ?? []
  const [editing, setEditing] = useState(null)
  const [deleting, setDeleting] = useState(null)
  const [note, setNote] = useState(null)

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-extrabold">Categories</h1>
          <p className="text-ink-2 mt-1 text-sm">
            How the app’s Games tab groups the store, in this order. Each game carries one, set on its page.
          </p>
        </div>
        <button type="button" className="btn btn-primary" disabled={!canWrite} onClick={() => setEditing({})}>
          <Plus className="size-4" /> New category
        </button>
      </div>

      {note && (
        <div className="mt-4">
          <Note tone="good">{note}</Note>
        </div>
      )}

      <div className="card mt-5 overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="text-ink-3 border-line bg-surface-2/60 border-b text-left text-xs">
            <tr>
              <th className="px-4 py-2 text-right">Order</th>
              <th className="px-4 py-2">Key</th>
              <th className="px-4 py-2">English</th>
              <th className="px-4 py-2">Arabic</th>
              <th className="px-4 py-2 text-right">Games</th>
              {canWrite && <th className="px-4 py-2" aria-label="Actions" />}
            </tr>
          </thead>
          <tbody>
            {list.isLoading && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={6}>
                  Loading…
                </td>
              </tr>
            )}
            {list.isError && (
              <tr>
                <td className="text-bad px-4 py-6" colSpan={6}>
                  {list.error.message}
                </td>
              </tr>
            )}
            {rows.map((c) => (
              <tr key={c.id} className="border-line border-b last:border-0">
                <td className="text-ink-2 px-4 py-2 text-right tabular-nums">{c.position}</td>
                <td className="px-4 py-2 font-mono text-xs" dir="ltr">
                  {c.id}
                </td>
                <td className="px-4 py-2 font-semibold">{c.name_en}</td>
                <td className="px-4 py-2" dir="rtl" lang="ar">
                  {c.name_ar}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">{c.games}</td>
                {canWrite && (
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-1">
                      <button
                        type="button"
                        className="btn btn-secondary h-8 px-2.5 text-xs"
                        aria-label={`Edit ${c.name_en}`}
                        onClick={() => setEditing(c)}
                      >
                        <Pencil className="size-3.5" /> Edit
                      </button>
                      {c.id !== 'other' && (
                        <button
                          type="button"
                          className="btn btn-secondary text-bad h-8 px-2.5 text-xs"
                          aria-label={`Delete ${c.name_en}`}
                          onClick={() => setDeleting(c)}
                        >
                          <Trash2 className="size-3.5" />
                        </button>
                      )}
                    </div>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-ink-3 mt-3 text-xs">
        Phones on an app version from before 2026-11-24 know only the first eight, and show any other category as Other.
      </p>

      {editing && <CategoryForm category={editing} onClose={() => setEditing(null)} onDone={setNote} />}
      {deleting && <DeleteCategory category={deleting} onClose={() => setDeleting(null)} onDone={setNote} />}
    </div>
  )
}

// A new category (no id yet) or an existing one's names and order. The key
// is chosen once: the games carry it.
function CategoryForm({ category: c, onClose, onDone }) {
  const queries = useQueryClient()
  const fresh = !c.id
  const [id, setId] = useState('')
  const [nameAR, setNameAR] = useState(c.name_ar ?? '')
  const [nameEN, setNameEN] = useState(c.name_en ?? '')
  const [position, setPosition] = useState(c.position === undefined ? '' : String(c.position))
  const save = useMutation({
    mutationFn: () => {
      const body = { name_ar: nameAR.trim(), name_en: nameEN.trim() }
      if (position.trim() !== '') body.position = Number(position)
      return fresh
        ? api.post('/v1/admin/categories', { id: id.trim(), ...body })
        : api.patch(`/v1/admin/categories/${c.id}`, body)
    },
    onSuccess: (saved) => {
      queries.invalidateQueries({ queryKey: ['categories'] })
      onDone(fresh ? `Made ${saved.name_en}.` : `Saved ${saved.name_en}.`)
      onClose()
    },
  })
  const ready = nameAR.trim() && nameEN.trim() && (!fresh || id.trim())

  return (
    <Dialog
      open
      onClose={onClose}
      title={fresh ? 'New category' : `Edit ${c.name_en}`}
      description={
        fresh
          ? 'A key the games carry, and the name the app shows in each language.'
          : 'The names show in the app from its next read of the store. The key stays.'
      }
      footer={
        <>
          <button type="button" className="btn btn-secondary" onClick={onClose} disabled={save.isPending}>
            Cancel
          </button>
          <button type="submit" form="category-form" className="btn btn-primary" disabled={save.isPending || !ready}>
            {save.isPending ? 'Saving…' : fresh ? 'Make it' : 'Save'}
          </button>
        </>
      }
    >
      <form
        id="category-form"
        className="grid gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (ready) save.mutate()
        }}
      >
        {fresh ? (
          <label className="grid gap-1 text-sm font-semibold">
            Key
            <input
              className="field font-mono font-normal"
              dir="ltr"
              maxLength={32}
              placeholder="cards"
              value={id}
              onChange={(e) => setId(e.target.value.toLowerCase())}
            />
            <span className="text-ink-3 text-xs font-normal">a–z, 0–9 and -, from a letter. It cannot change later.</span>
          </label>
        ) : (
          <div className="text-sm">
            <span className="label">Key</span>{' '}
            <span className="font-mono" dir="ltr">
              {c.id}
            </span>
          </div>
        )}
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="grid gap-1 text-sm font-semibold">
            English name
            <input className="field font-normal" maxLength={40} value={nameEN} onChange={(e) => setNameEN(e.target.value)} />
          </label>
          <label className="grid gap-1 text-sm font-semibold">
            Arabic name
            <input
              className="field font-normal"
              dir="rtl"
              lang="ar"
              maxLength={40}
              value={nameAR}
              onChange={(e) => setNameAR(e.target.value)}
            />
          </label>
        </div>
        <label className="grid gap-1 text-sm font-semibold">
          Order
          <input
            className="field font-normal sm:max-w-40"
            type="number"
            min={0}
            max={10000}
            step={1}
            placeholder={fresh ? 'after the others' : ''}
            value={position}
            onChange={(e) => setPosition(e.target.value)}
          />
          <span className="text-ink-3 text-xs font-normal">Smallest first. Other is at 1000, so it stays last.</span>
        </label>
        {save.isError && <p className="text-bad text-sm">{save.error.message}</p>}
      </form>
    </Dialog>
  )
}

function DeleteCategory({ category: c, onClose, onDone }) {
  const queries = useQueryClient()
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/admin/categories/${c.id}`),
    onSuccess: (r) => {
      queries.invalidateQueries({ queryKey: ['categories'] })
      queries.invalidateQueries({ queryKey: ['games'] })
      onDone(`Deleted ${c.name_en}. ${r.moved === 1 ? '1 game' : `${r.moved} games`} moved to Other.`)
      onClose()
    },
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Delete ${c.name_en}?`}
      description={
        c.games
          ? `${c.games === 1 ? 'Its game moves' : `Its ${c.games} games move`} to Other, and the app stops showing ${c.name_en}.`
          : `No game carries it. The app stops showing ${c.name_en}.`
      }
      footer={
        <>
          <button type="button" className="btn btn-secondary" onClick={onClose} disabled={remove.isPending}>
            Cancel
          </button>
          <button type="button" className="btn btn-primary" disabled={remove.isPending} onClick={() => remove.mutate()}>
            {remove.isPending ? 'Deleting…' : 'Yes, delete it'}
          </button>
        </>
      }
    >
      {remove.isError && <p className="text-bad text-sm">{remove.error.message}</p>}
    </Dialog>
  )
}
