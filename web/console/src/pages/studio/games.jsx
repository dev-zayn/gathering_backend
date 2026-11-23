import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { Plus, Users } from 'lucide-react'

import { api } from '../../api.js'
import Dialog from '../../components/dialog.jsx'
import { BuildBadge } from '../../components/build-bits.jsx'
import { ListingBadge, SecretOnce, Tile } from '../../components/game-bits.jsx'
import { useMe } from '../../lib/me.js'

// A publisher's games (docs/PUBLISHERS.md): each with what is live and where
// its next version stands, and the one way in for a new one, its manifest.
export default function StudioGames() {
  const me = useMe()
  const list = useQuery({ queryKey: ['studio', 'games'], queryFn: () => api.get('/v1/publisher/games') })
  const rows = list.data?.items ?? []

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-extrabold">My games</h1>
          <p className="text-ink-2 mt-1 text-sm">
            {me?.name}’s games on گعدة: what players have, and the next version on its way.
          </p>
        </div>
        <Register />
      </div>

      {list.isLoading && <p className="text-ink-3 mt-6 text-sm">Loading…</p>}
      {list.isError && <p className="text-bad mt-6 text-sm">{list.error.message}</p>}
      {list.data && rows.length === 0 && <FirstSteps />}

      <div className="mt-5 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {rows.map((g) => (
          <Link key={g.id} to={`/games/${g.id}`} className="card hover:border-line-2 flex flex-col gap-3 p-4 transition-colors">
            <div className="flex items-center gap-3">
              <Tile src={g.icon || g.build?.store?.icon} name={g.name} size="size-12" />
              <div className="min-w-0">
                <div className="truncate font-semibold">{g.name}</div>
                <div className="text-ink-2 truncate font-mono text-xs" dir="ltr">
                  {g.id}
                </div>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <ListingBadge listing={g.listing} />
              {g.version && <span className="text-ink-2 font-mono">live {g.version}</span>}
            </div>
            <div className="border-line flex items-center justify-between gap-2 border-t pt-3 text-xs">
              {g.build ? (
                <span className="flex items-center gap-1.5">
                  <span className="text-ink-2 font-mono">{g.build.version}</span>
                  <BuildBadge status={g.build.status} />
                </span>
              ) : (
                <span className="text-ink-3">No version on its way</span>
              )}
              <span className="text-ink-2 inline-flex items-center gap-1">
                <Users className="size-3.5" /> {g.testers} testers
              </span>
            </div>
          </Link>
        ))}
      </div>
    </div>
  )
}

// What a studio with no game yet needs to know, in order.
function FirstSteps() {
  const steps = [
    ['Host your game', 'Put the game and its manifest at /.well-known/gathering-game.json on a server of yours.'],
    ['Register it here', 'Give us the address. We read the manifest; nothing about the game is typed here.'],
    ['Invite testers', 'Make a code and share it. Testers type it in the app: Me → Beta games.'],
    ['Send it to review', 'Add a tagline and an icon, then send it. Once approved, you publish it.'],
  ]
  return (
    <ol className="mt-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {steps.map(([title, text], i) => (
        <li key={title} className="card p-4">
          <span className="bg-accent text-accent-ink inline-flex size-6 items-center justify-center rounded-full font-mono text-xs font-bold">
            {i + 1}
          </span>
          <div className="mt-2 text-sm font-semibold">{title}</div>
          <p className="text-ink-2 mt-1 text-xs">{text}</p>
        </li>
      ))}
    </ol>
  )
}

// Registering is reading a document: give us the address, we fetch and check
// it, and hand back the client secret, once.
function Register() {
  const queries = useQueryClient()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [url, setUrl] = useState('')
  const register = useMutation({
    mutationFn: () => api.post('/v1/publisher/games', { url: url.trim() }),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['studio'] }),
  })
  const made = register.data

  function close() {
    setOpen(false)
    setUrl('')
    register.reset()
  }

  return (
    <>
      <button type="button" className="btn btn-primary" onClick={() => setOpen(true)}>
        <Plus className="size-4" /> Register a game
      </button>
      <Dialog
        open={open}
        onClose={close}
        title={made ? made.game.name : 'Register a game'}
        description={
          made
            ? 'Registered. Its first version is under test: invite testers, then send it to review.'
            : 'Your game’s address. We read its manifest and check everything in it at once.'
        }
        footer={
          made ? (
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => {
                const id = made.game.id
                close()
                navigate(`/games/${id}`)
              }}
            >
              I saved the secret — open the game
            </button>
          ) : (
            <>
              <button type="button" className="btn btn-secondary" onClick={close} disabled={register.isPending}>
                Cancel
              </button>
              <button type="submit" form="studio-register" className="btn btn-primary" disabled={register.isPending || !url.trim()}>
                {register.isPending ? 'Reading…' : 'Read the manifest'}
              </button>
            </>
          )
        }
      >
        {made ? (
          <div className="grid gap-3">
            <SecretOnce secret={made.client_secret} hint="Copy it now: it cannot be read back, only rolled again." />
            <p className="text-ink-2 text-xs">
              Your game’s server trades a player’s one-time code for a token with this secret. Keep it on your
              server, never in the game’s page.
            </p>
          </div>
        ) : (
          <form
            id="studio-register"
            className="grid gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              register.mutate()
            }}
          >
            <label className="text-sm font-semibold" htmlFor="studio-manifest">
              Manifest URL
            </label>
            <input
              id="studio-manifest"
              className="field font-mono text-xs"
              type="url"
              dir="ltr"
              autoFocus
              autoComplete="off"
              spellCheck={false}
              placeholder="https://games.yourstudio.example/mygame/"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
            />
            <p className="text-ink-2 text-xs">
              The manifest’s address, or the game’s origin: /.well-known/gathering-game.json is added to an
              origin. Its id becomes the game’s id for good.
            </p>
            {register.isError && <p className="text-bad text-sm">{register.error.message}</p>}
          </form>
        )}
      </Dialog>
    </>
  )
}
