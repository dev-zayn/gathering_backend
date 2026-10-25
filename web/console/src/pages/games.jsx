import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { Plus } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Fact, ListingBadge, SecretOnce, Tag, Tile } from '../components/game-bits.jsx'
import { LISTINGS, needs, tierLabel, when } from '../lib/games.js'

// The registry: every game in any listing, and the one way in — a URL.
export default function Games() {
  const [params, setParams] = useSearchParams()
  const asked = params.get('listing')
  const listing = LISTINGS.includes(asked) ? asked : 'all'
  const [q, setQ] = useState('')

  const list = useQuery({ queryKey: ['games'], queryFn: () => api.get('/v1/admin/games') })
  // A failed count does not take the list with it.
  const reach = useQuery({ queryKey: ['games', 'reach'], queryFn: () => api.get('/v1/admin/games/reach') })
  const shelves = Object.fromEntries((reach.data?.games ?? []).map((r) => [r.game, r.players]))

  const term = q.trim().toLowerCase()
  const rows = (list.data?.games ?? [])
    .filter((g) => listing === 'all' || g.listing === listing)
    .filter(
      (g) =>
        !term ||
        g.name.toLowerCase().includes(term) ||
        g.id.toLowerCase().includes(term) ||
        (g.publisher ?? '').toLowerCase().includes(term),
    )

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-extrabold">Games</h1>
          <p className="text-ink-2 mt-1 text-sm">Every game in the registry, and whether players can see it.</p>
        </div>
        <Register />
      </div>

      <div className="mt-4 flex flex-wrap items-center gap-2">
        {LISTINGS.map((l) => (
          <button
            key={l}
            type="button"
            className="chip"
            aria-pressed={l === listing}
            onClick={() => setParams(l === 'all' ? {} : { listing: l })}
          >
            {l}
          </button>
        ))}
        <input
          className="field ms-auto max-w-xs"
          placeholder="Name, id, publisher…"
          aria-label="Search games"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      </div>

      <div className="card mt-4 overflow-hidden">
        <table className="w-full text-sm">
          <thead className="text-ink-3 border-line bg-surface-2/60 border-b text-left text-xs">
            <tr>
              <th className="px-4 py-2">Game</th>
              <th className="px-4 py-2">Publisher</th>
              <th className="px-4 py-2">Listing</th>
              <th className="px-4 py-2">Store copy</th>
              <th className="px-4 py-2 text-right">Shelves</th>
              <th className="px-4 py-2">Version</th>
              <th className="px-4 py-2">Last read</th>
            </tr>
          </thead>
          <tbody>
            {list.isLoading && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={7}>
                  Loading…
                </td>
              </tr>
            )}
            {list.isError && (
              <tr>
                <td className="text-bad px-4 py-6" colSpan={7}>
                  {list.error.message}
                </td>
              </tr>
            )}
            {list.data && rows.length === 0 && (
              <tr>
                <td className="text-ink-3 px-4 py-6" colSpan={7}>
                  {list.data.games.length === 0 ? 'The registry is empty. Register a game by its URL.' : 'Nothing matches.'}
                </td>
              </tr>
            )}
            {rows.map((g) => {
              const missing = needs(g)
              return (
                <tr key={g.id} className="border-line border-b last:border-0">
                  <td className="px-4 py-2">
                    <Link to={`/games/${g.id}`} className="flex items-center gap-3 hover:underline">
                      <Tile src={g.icon} name={g.name} size="size-8" />
                      <span className="min-w-0">
                        <span className="block font-semibold">{g.name}</span>
                        <span className="text-ink-2 block font-mono text-xs">{g.id}</span>
                      </span>
                    </Link>
                  </td>
                  <td className="px-4 py-2">
                    <span className="block">{g.publisher || '—'}</span>
                    <span className="text-ink-2 block text-xs">{tierLabel[g.tier] ?? g.tier}</span>
                  </td>
                  <td className="px-4 py-2">
                    <span className="flex items-center gap-1.5">
                      <ListingBadge listing={g.listing} />
                      {g.featured && <Tag tone="bg-accent/15 text-accent-text">Featured</Tag>}
                      {g.held && <Tag tone="bg-warn/10 text-warn">Held</Tag>}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-xs">
                    {missing ? <span className="text-warn font-semibold">{missing}</span> : <span className="text-ink-2">Written</span>}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums">{shelves[g.id] ?? 0}</td>
                  <td className="px-4 py-2 font-mono text-xs">{g.version}</td>
                  <td className="text-ink-2 px-4 py-2 text-xs">{when(g.fetched_at)}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// Registering is reading a document: give us the address, we fetch and
// check it, and hand back the secret — once.
function Register() {
  const queries = useQueryClient()
  const [open, setOpen] = useState(false)
  const [url, setUrl] = useState('')
  const register = useMutation({
    mutationFn: () => api.post('/v1/admin/games', { url: url.trim() }),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['games'] }),
  })
  const registered = register.data

  function close() {
    setOpen(false)
    setUrl('')
    register.reset()
  }

  return (
    <>
      <button type="button" className="btn btn-primary" onClick={() => setOpen(true)}>
        <Plus className="size-4" /> Register by URL
      </button>
      <Dialog
        open={open}
        onClose={close}
        title={registered ? registered.name : 'Register a game'}
        description={
          registered
            ? 'Registered, and unpublished. Below is what the manifest said, and the secret its server needs.'
            : "The game's address. We read its manifest — nothing about the game is typed here."
        }
        footer={
          registered ? (
            <>
              <button type="button" className="btn btn-secondary" onClick={close}>
                Done
              </button>
              <Link to={`/games/${registered.id}`} className="btn btn-primary" onClick={close}>
                Open its page
              </Link>
            </>
          ) : (
            <>
              <button type="button" className="btn btn-secondary" onClick={close} disabled={register.isPending}>
                Cancel
              </button>
              <button
                type="submit"
                form="register-game"
                className="btn btn-primary"
                disabled={register.isPending || !url.trim()}
              >
                {register.isPending ? 'Reading…' : 'Read the manifest'}
              </button>
            </>
          )
        }
      >
        {registered ? (
          <div className="grid gap-4">
            <SecretOnce secret={registered.client_secret} />
            <div className="grid grid-cols-2 gap-3">
              <Fact label="ID" mono>
                {registered.id}
              </Fact>
              <Fact label="Version" mono>
                {registered.version}
              </Fact>
              <Fact label="Publisher">{registered.publisher}</Fact>
              <Fact label="Scopes">{registered.scopes?.join(', ')}</Fact>
            </div>
            <Fact label="Entry" mono>
              {registered.entry}
            </Fact>
          </div>
        ) : (
          <form
            id="register-game"
            className="grid gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              register.mutate()
            }}
          >
            <label className="text-sm font-semibold" htmlFor="manifest-url">
              Manifest URL
            </label>
            <input
              id="manifest-url"
              className="field font-mono text-xs"
              type="url"
              dir="ltr"
              autoFocus
              autoComplete="off"
              spellCheck={false}
              placeholder="https://games.yourstudio.example/xo/"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
            />
            <p className="text-ink-2 text-xs">
              The manifest's address, or the game's origin — an origin gets /.well-known/gathering-game.json
              appended. Everything wrong with it comes back at once.
            </p>
            {register.isError && <p className="text-bad text-sm">{register.error.message}</p>}
          </form>
        )}
      </Dialog>
    </>
  )
}
