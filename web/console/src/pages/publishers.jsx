import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Check, Copy, Plus } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Note } from '../components/game-bits.jsx'
import { publisherTone } from '../lib/builds.js'
import { when } from '../lib/games.js'
import { useMe } from '../lib/me.js'

// The studios that make games for گعدة (docs/PUBLISHERS.md): each one sign-in
// to this console, which sees its own games only.
export default function Publishers() {
  const list = useQuery({ queryKey: ['publishers'], queryFn: () => api.get('/v1/admin/publishers') })
  const rows = list.data?.items ?? []

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-extrabold">Publishers</h1>
          <p className="text-ink-2 mt-1 text-sm">
            Studios that register their own games, test them with players they invite and send them to review.
          </p>
        </div>
        <AddPublisher />
      </div>

      <div className="card mt-5 overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="text-ink-3 border-line bg-surface-2/60 border-b text-left text-xs">
            <tr>
              <th className="px-4 py-2">Studio</th>
              <th className="px-4 py-2">Status</th>
              <th className="px-4 py-2 text-right">Games</th>
              <th className="px-4 py-2">Last sign-in</th>
              <th className="px-4 py-2">Since</th>
            </tr>
          </thead>
          <tbody>
            {list.isLoading && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={5}>
                  Loading…
                </td>
              </tr>
            )}
            {list.isError && (
              <tr>
                <td className="text-bad px-4 py-6" colSpan={5}>
                  {list.error.message}
                </td>
              </tr>
            )}
            {list.data && rows.length === 0 && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={5}>
                  No publisher yet. Add a studio to give it its own sign-in.
                </td>
              </tr>
            )}
            {rows.map((p) => (
              <tr key={p.id} className="border-line border-b last:border-0">
                <td className="px-4 py-2">
                  <Link to={`/publishers/${p.id}`} className="block hover:underline">
                    <span className="block font-semibold">{p.name}</span>
                    <span className="text-ink-2 block text-xs" dir="ltr">
                      {p.email}
                    </span>
                  </Link>
                </td>
                <td className="px-4 py-2">
                  <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-semibold ${publisherTone[p.status] ?? ''}`}>
                    {p.status}
                  </span>
                </td>
                <td className="px-4 py-2 text-right tabular-nums">{p.games}</td>
                <td className="text-ink-2 px-4 py-2 text-xs">{when(p.seen_at)}</td>
                <td className="text-ink-2 px-4 py-2 text-xs">{when(p.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// A new studio account: a name and the email it signs in with. The answer is
// a one-time link the admin sends, by whatever means: there is no email from
// here.
function AddPublisher() {
  const me = useMe()
  const queries = useQueryClient()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const add = useMutation({
    mutationFn: () => api.post('/v1/admin/publishers', { name: name.trim(), email: email.trim() }),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['publishers'] }),
  })
  const made = add.data
  const canWrite = me?.roles?.includes('admin')

  function close() {
    setOpen(false)
    setName('')
    setEmail('')
    add.reset()
  }

  return (
    <>
      <button type="button" className="btn btn-primary" disabled={!canWrite} onClick={() => setOpen(true)}>
        <Plus className="size-4" /> Add a publisher
      </button>
      <Dialog
        open={open}
        onClose={close}
        title={made ? made.publisher.name : 'Add a publisher'}
        description={
          made
            ? 'Made, with no password yet. Send this link to the studio: it chooses its password there.'
            : 'The studio’s name, as its games will show it, and the email it signs in with.'
        }
        footer={
          made ? (
            <button type="button" className="btn btn-primary" onClick={close}>
              Done
            </button>
          ) : (
            <>
              <button type="button" className="btn btn-secondary" onClick={close} disabled={add.isPending}>
                Cancel
              </button>
              <button
                type="submit"
                form="add-publisher"
                className="btn btn-primary"
                disabled={add.isPending || !name.trim() || !email.trim()}
              >
                {add.isPending ? 'Adding…' : 'Add'}
              </button>
            </>
          )
        }
      >
        {made ? (
          <SetupLink setup={made.setup} />
        ) : (
          <form
            id="add-publisher"
            className="grid gap-3"
            onSubmit={(e) => {
              e.preventDefault()
              add.mutate()
            }}
          >
            <label className="grid gap-1 text-sm font-semibold">
              Studio name
              <input className="field font-normal" maxLength={120} autoFocus value={name} onChange={(e) => setName(e.target.value)} />
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
            {add.isError && <p className="text-bad text-sm">{add.error.message}</p>}
          </form>
        )}
      </Dialog>
    </>
  )
}

// The one-time link, shown once, made from this console's own address.
export function SetupLink({ setup }) {
  const [copied, setCopied] = useState(false)
  const link = `${location.origin}/setup#${setup.token}`
  async function copy() {
    try {
      await navigator.clipboard.writeText(link)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }
  return (
    <Note tone="wait">
      <b>Sign-in link — shown once, works once.</b>
      <div className="mt-2 flex items-center gap-2">
        <code
          className="bg-surface border-line min-w-0 flex-1 rounded-md border px-2 py-1.5 font-mono text-xs [overflow-wrap:anywhere]"
          dir="ltr"
        >
          {link}
        </code>
        <button type="button" className="btn btn-secondary h-8 px-3 text-xs" onClick={copy}>
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <p className="text-ink-2 mt-2 text-xs">It lasts until {when(setup.expires_at)}. A new one replaces it.</p>
    </Note>
  )
}
