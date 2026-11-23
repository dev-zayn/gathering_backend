import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, Ban, KeyRound, Power } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { BuildBadge } from '../components/build-bits.jsx'
import { Fact, ListingBadge, Note, Tile } from '../components/game-bits.jsx'
import { publisherTone } from '../lib/builds.js'
import { when } from '../lib/games.js'
import { useMe } from '../lib/me.js'
import { SetupLink } from './publishers.jsx'

// One studio's account: who it is, its games, a new sign-in link, and the
// switch that ends its sessions (docs/PUBLISHERS.md).
export default function Publisher() {
  const { id } = useParams()
  const me = useMe()
  const canWrite = me?.roles?.includes('admin')
  const queries = useQueryClient()
  const page = useQuery({ queryKey: ['publisher', id], queryFn: () => api.get(`/v1/admin/publishers/${id}`) })
  const [setup, setSetup] = useState(null)
  const changed = () => {
    queries.invalidateQueries({ queryKey: ['publisher', id] })
    queries.invalidateQueries({ queryKey: ['publishers'] })
  }
  const link = useMutation({ mutationFn: () => api.post(`/v1/admin/publishers/${id}/setup`), onSuccess: (r) => setSetup(r) })
  const toggle = useMutation({
    mutationFn: (on) => api.post(`/v1/admin/publishers/${id}/${on ? 'enable' : 'disable'}`),
    onSuccess: changed,
  })

  if (page.isLoading) return <p className="text-ink-3 text-sm">Loading…</p>
  if (page.isError)
    return (
      <div>
        <p className="text-bad text-sm">{page.error.message}</p>
        <Link to="/publishers" className="btn btn-secondary mt-4">
          <ArrowLeft className="size-4" /> Publishers
        </Link>
      </div>
    )
  const p = page.data.publisher
  const games = page.data.games ?? []
  const error = (link.error ?? toggle.error)?.message

  return (
    <div className="grid gap-5">
      <div>
        <Link to="/publishers" className="text-ink-2 hover:text-ink inline-flex items-center gap-1 text-xs font-semibold">
          <ArrowLeft className="size-3.5" /> Publishers
        </Link>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-2xl font-extrabold">{p.name}</h1>
            <div className="text-ink-2 mt-0.5 flex flex-wrap items-center gap-2 text-sm">
              <span dir="ltr">{p.email}</span>
              <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold ${publisherTone[p.status] ?? ''}`}>
                {p.status}
              </span>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Edit publisher={p} disabled={!canWrite} onSaved={changed} />
            <button type="button" className="btn btn-secondary" disabled={!canWrite || link.isPending} onClick={() => link.mutate()}>
              <KeyRound className="size-4" /> {p.status === 'invited' ? 'A new sign-in link' : 'A password reset link'}
            </button>
            {p.status === 'disabled' ? (
              <button type="button" className="btn btn-secondary" disabled={!canWrite || toggle.isPending} onClick={() => toggle.mutate(true)}>
                <Power className="size-4" /> Switch back on
              </button>
            ) : (
              <button
                type="button"
                className="btn btn-secondary text-bad"
                disabled={!canWrite || toggle.isPending}
                onClick={() => {
                  if (confirm(`Switch ${p.name} off? Every session it holds ends now. Its games stay as they are.`)) toggle.mutate(false)
                }}
              >
                <Ban className="size-4" /> Switch off
              </button>
            )}
          </div>
        </div>
      </div>

      {error && <Note tone="bad">{error}</Note>}
      {setup && <SetupLink setup={setup} />}
      {p.status === 'invited' && !setup && (
        <Note tone="wait">
          No password yet.{' '}
          {p.setup_expires_at ? `Its sign-in link works until ${when(p.setup_expires_at)}.` : 'Its link has expired: make a new one.'}
        </Note>
      )}
      {p.status === 'disabled' && <Note tone="bad">Switched off: it cannot sign in. Its games are untouched.</Note>}

      <section className="card grid gap-3 p-4 sm:grid-cols-3">
        <Fact label="Games">{String(p.games)}</Fact>
        <Fact label="Last sign-in">{when(p.seen_at)}</Fact>
        <Fact label="Since">{when(p.created_at)}</Fact>
      </section>

      <section className="card p-4">
        <h2 className="text-sm font-extrabold">Games</h2>
        {games.length === 0 ? (
          <p className="text-ink-3 mt-3 text-sm">None registered yet.</p>
        ) : (
          <ul className="mt-3 grid gap-2">
            {games.map((g) => (
              <li key={g.id} className="border-line flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2">
                <Link to={`/games/${g.id}`} className="flex min-w-0 items-center gap-3 hover:underline">
                  <Tile src={g.icon} name={g.name} size="size-8" />
                  <span className="min-w-0">
                    <span className="block truncate font-semibold">{g.name}</span>
                    <span className="text-ink-2 block font-mono text-xs" dir="ltr">
                      {g.id} · {g.version}
                    </span>
                  </span>
                </Link>
                <span className="flex items-center gap-1.5">
                  <ListingBadge listing={g.listing} />
                  {g.build_status && <BuildBadge status={g.build_status} />}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

// Renaming a studio, or moving its sign-in to another address.
function Edit({ publisher: p, disabled, onSaved }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(p.name)
  const [email, setEmail] = useState(p.email)
  const save = useMutation({
    mutationFn: () => api.patch(`/v1/admin/publishers/${p.id}`, { name: name.trim(), email: email.trim() }),
    onSuccess: () => {
      setOpen(false)
      onSaved()
    },
  })
  return (
    <>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={disabled}
        onClick={() => {
          setName(p.name)
          setEmail(p.email)
          save.reset()
          setOpen(true)
        }}
      >
        Edit
      </button>
      <Dialog
        open={open}
        onClose={() => !save.isPending && setOpen(false)}
        title={`Edit ${p.name}`}
        description="A new name reaches its games when it next publishes."
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setOpen(false)} disabled={save.isPending}>
              Cancel
            </button>
            <button type="submit" form="edit-publisher" className="btn btn-primary" disabled={save.isPending || !name.trim() || !email.trim()}>
              {save.isPending ? 'Saving…' : 'Save'}
            </button>
          </>
        }
      >
        <form
          id="edit-publisher"
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            save.mutate()
          }}
        >
          <label className="grid gap-1 text-sm font-semibold">
            Studio name
            <input className="field font-normal" maxLength={120} value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="grid gap-1 text-sm font-semibold">
            Email
            <input
              className="field font-normal"
              type="email"
              dir="ltr"
              maxLength={254}
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </label>
          {save.isError && <p className="text-bad text-sm">{save.error.message}</p>}
        </form>
      </Dialog>
    </>
  )
}
