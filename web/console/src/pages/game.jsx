import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, Ban, KeyRound, RefreshCw, Sparkles, Upload, X } from 'lucide-react'

import { api } from '../api.js'
import Dialog from '../components/dialog.jsx'
import { Fact, ListingBadge, Note, SecretOnce, Tag, Tile } from '../components/game-bits.jsx'
import { categoryLabel, needs, overdue, roomFacts, tierLabel, when } from '../lib/games.js'

// One game: what its manifest says, where the document lives, the store copy
// we write, and the switches that reach every player at once.
export default function Game() {
  const { id } = useParams()
  const queries = useQueryClient()
  const game = useQuery({ queryKey: ['game', id], queryFn: () => api.get(`/v1/admin/games/${id}`) })
  const reach = useQuery({ queryKey: ['games', 'reach'], queryFn: () => api.get('/v1/admin/games/reach') })
  const [secret, setSecret] = useState(null)
  const [note, setNote] = useState(null)

  function changed() {
    queries.invalidateQueries({ queryKey: ['game', id] })
    queries.invalidateQueries({ queryKey: ['games'] })
  }

  // One action at a time, each reaching every player: it says how it went,
  // and the page stays busy until it shows the result.
  const action = useMutation({
    mutationFn: ({ run }) => run(),
    onSuccess: (r, { name, said }) => {
      if (name === 'secret') setSecret(r.client_secret)
      setNote({ tone: 'good', text: said })
      changed()
    },
    onError: (e) => setNote({ tone: 'bad', text: e.message }),
  })
  const busy = action.isPending ? action.variables.name : null
  const act = (name, said, run) => action.mutate({ name, said, run })

  if (game.isLoading) return <p className="text-ink-3 text-sm">Loading…</p>
  if (game.isError)
    return (
      <div>
        <p className="text-bad text-sm">{game.error.message}</p>
        <Link to="/games" className="btn btn-secondary mt-4">
          <ArrowLeft className="size-4" /> Games
        </Link>
      </div>
    )
  const g = game.data
  const shelves = reach.data?.games?.find((r) => r.game === id)
  const missing = needs(g)

  return (
    <div className="grid gap-5">
      <div>
        <Link to="/games" className="text-ink-2 hover:text-ink inline-flex items-center gap-1 text-xs font-semibold">
          <ArrowLeft className="size-3.5" /> Games
        </Link>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="flex items-center gap-4">
            <Tile src={g.icon} name={g.name} size="size-16" />
            <div>
              <h1 className="text-xl font-extrabold">{g.name}</h1>
              <div className="text-ink-2 mt-0.5 flex flex-wrap items-center gap-2 text-sm">
                <span className="font-mono text-xs" dir="ltr">
                  {g.id} · {g.version}
                </span>
                <ListingBadge listing={g.listing} />
                {g.featured && <Tag tone="bg-accent/20 text-accent-ink">Featured</Tag>}
                {shelves && <span className="text-xs">on {shelves.players} shelves</span>}
              </div>
            </div>
          </div>
          {g.listing !== 'published' && (
            <button
              type="button"
              className="btn btn-primary"
              disabled={Boolean(busy) || Boolean(missing)}
              title={missing ?? undefined}
              onClick={() => {
                if (confirm(`Publish ${g.name}? It appears in every store the moment you confirm.`))
                  act('publish', 'Published to the store', () => api.post(`/v1/admin/games/${id}/publish`))
              }}
            >
              <Upload className="size-4" /> Publish to the store
            </button>
          )}
        </div>
      </div>

      {note && (
        <Note tone={note.tone}>
          <span className="flex items-center justify-between gap-3">
            {note.text}
            <button type="button" aria-label="Dismiss" className="text-ink-3 hover:text-ink" onClick={() => setNote(null)}>
              <X className="size-3.5" />
            </button>
          </span>
        </Note>
      )}
      {secret && <SecretOnce secret={secret} />}
      {g.listing === 'suspended' && (
        <Note tone="bad">Out of every store and shelf, tokens stopped. Shelves were kept, so publishing puts it back.</Note>
      )}
      {g.listing === 'unpublished' && (
        <Note tone="wait">{missing ? `${missing} before it can be published.` : 'A 404 to every player until somebody publishes it.'}</Note>
      )}
      {g.held && <Held game={g} busy={busy} onAccept={() => act('accept', `Version ${g.held.version} is live`, () => api.post(`/v1/admin/games/${id}/accept`))} />}

      <div className="grid gap-5 lg:grid-cols-3">
        <Manifest game={g} />
        <Room game={g} />
        <Source game={g} busy={busy} onRefresh={() => act('refresh', 'Manifest re-read', () => api.post(`/v1/admin/games/${id}/refresh`))} onMoved={changed} />
      </div>

      <div className="grid gap-5 lg:grid-cols-3">
        <StoreCopy key={copyKey(g)} game={g} onSaved={changed} className="lg:col-span-2" />
        <Actions game={g} busy={busy} act={act} />
      </div>

      <Pictures game={g} onChanged={changed} />
    </div>
  )
}

const copyKey = (g) => [g.tagline, g.store?.description, g.category, g.featured].join('|')

function Section({ title, children, className = '' }) {
  return (
    <section className={`card p-4 ${className}`}>
      <h2 className="text-sm font-extrabold">{title}</h2>
      <div className="mt-3 grid gap-3">{children}</div>
    </section>
  )
}

// Read from the game's own document. Nothing here is typed on our side, so
// nothing here can be wrong on our side either.
function Manifest({ game: g }) {
  return (
    <Section title="What the manifest says">
      <Fact label="Publisher">{g.publisher}</Fact>
      <div className="grid grid-cols-2 gap-3">
        <Fact label="Made by">{tierLabel[g.tier] ?? g.tier}</Fact>
        <Fact label="Version" mono>
          {g.version}
        </Fact>
        <Fact label="Orientation">{g.orientation}</Fact>
        <Fact label="Splash">{g.splash ? 'Covered until ready' : 'No'}</Fact>
      </div>
      <Fact label="Entry" mono>
        <a href={g.entry} target="_blank" rel="noopener noreferrer" className="hover:underline">
          {g.entry}
        </a>
      </Fact>
      <Fact label="Scopes">{g.scopes?.length ? g.scopes.join(', ') : 'Identity and nothing more.'}</Fact>
      <Fact label="Permissions">{g.permissions?.length ? g.permissions.join(', ') : 'None.'}</Fact>
      {g.redirect_uris?.length > 0 && (
        <Fact label="Redirects" mono>
          {g.redirect_uris.join('\n')}
        </Fact>
      )}
    </Section>
  )
}

// The room block, as facts: what every room for this game is made under.
function Room({ game: g }) {
  return (
    <Section title="The room">
      {roomFacts(g.room).map(([label, value]) => (
        <Fact key={label} label={label}>
          {value}
        </Fact>
      ))}
    </Section>
  )
}

// Where the document lives and when we last read it. A date far in the past
// is a fetch that is failing.
function Source({ game: g, busy, onRefresh, onMoved }) {
  const stale = overdue(g.fetched_at)
  return (
    <Section title="The document">
      <Fact label="Manifest URL" mono>
        <a href={g.manifest_url} target="_blank" rel="noopener noreferrer" className="hover:underline">
          {g.manifest_url}
        </a>
      </Fact>
      <Fact label="Last read">
        {when(g.fetched_at)}
        {stale && <span className="text-warn"> · overdue</span>}
      </Fact>
      <div className="flex flex-wrap gap-2">
        <button type="button" className="btn btn-secondary h-8 px-3 text-xs" disabled={Boolean(busy)} onClick={onRefresh}>
          <RefreshCw className="size-3.5" /> {busy === 'refresh' ? 'Reading…' : 'Re-read it now'}
        </button>
        <Move game={g} onMoved={onMoved} />
      </div>
      <p className="text-ink-3 text-xs">Re-read about once a day. A document asking for more waits here for you.</p>
    </Section>
  )
}

// A re-read that asked for more than the game has: new scopes, other
// permissions, another room. Nothing reaches a player until somebody reads
// the new document and says so.
function Held({ game: g, busy, onAccept }) {
  const h = g.held
  return (
    <Note tone="wait">
      <b>Version {h.version} asks for more.</b>{' '}
      <span className="text-ink-2">
        Changed: {h.changed.join(', ')}.
        {h.new.length > 0 && ` New scopes: ${h.new.join(', ')}.`} Players carry on with version {g.version} until you accept it.
      </span>
      <div className="mt-2 grid gap-1 text-xs">
        {h.changed.includes('permissions') && <span>Permissions would become: {h.permissions.join(', ') || 'none'}.</span>}
        {h.changed.includes('room') && (
          <span>
            Room would become:{' '}
            {roomFacts(h.room)
              .map(([label, value]) => `${label.toLowerCase()} ${value.toLowerCase()}`)
              .join('; ')}
            .
          </span>
        )}
      </div>
      <div className="mt-3">
        <button
          type="button"
          className="btn btn-primary h-8 px-3 text-xs"
          disabled={Boolean(busy)}
          onClick={() => {
            if (confirm(`Accept version ${h.version}? Every player's next token carries ${h.scopes.join(', ') || 'no scopes'}. Nobody is asked; you are the one reading it.`))
              onAccept()
          }}
        >
          Accept it
        </button>
      </div>
    </Note>
  )
}

// For a studio that moved its document. The service reads the new address
// and refuses it unless the manifest there still names this game.
function Move({ game: g, onMoved }) {
  const [open, setOpen] = useState(false)
  const [url, setUrl] = useState(g.manifest_url)
  const move = useMutation({
    mutationFn: () => api.put(`/v1/admin/games/${g.id}/manifest`, { url: url.trim() }),
    onSuccess: () => {
      setOpen(false)
      onMoved()
    },
  })
  const dirty = url.trim() !== '' && url.trim() !== g.manifest_url

  function toggle(next) {
    if (move.isPending) return
    setOpen(next)
    if (next) {
      setUrl(g.manifest_url)
      move.reset()
    }
  }

  return (
    <>
      <button type="button" className="btn btn-secondary h-8 px-3 text-xs" onClick={() => toggle(true)}>
        Move it
      </button>
      <Dialog
        open={open}
        onClose={() => toggle(false)}
        title="Move the manifest"
        description="The document at the new address must still name this game."
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => toggle(false)} disabled={move.isPending}>
              Cancel
            </button>
            <button type="submit" form="move-manifest" className="btn btn-primary" disabled={move.isPending || !dirty}>
              {move.isPending ? 'Reading…' : 'Read and move'}
            </button>
          </>
        }
      >
        <form
          id="move-manifest"
          className="grid gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (dirty) move.mutate()
          }}
        >
          <label className="text-sm font-semibold" htmlFor="move-url">
            New manifest URL
          </label>
          <input
            id="move-url"
            className="field font-mono text-xs"
            type="url"
            dir="ltr"
            autoComplete="off"
            spellCheck={false}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
          <p className="text-ink-2 text-xs">The game keeps its id, shelves and secret. Only the address changes.</p>
          {move.isError && <p className="text-bad text-sm">{move.error.message}</p>}
        </form>
      </Dialog>
    </>
  )
}

// The half no manifest carries, written by us so tiles read alike and no
// game describes its own permissions kindly. Remounted by the page whenever
// the record changes, so the form starts from what is on it.
function StoreCopy({ game: g, onSaved, className = '' }) {
  const [tagline, setTagline] = useState(g.tagline ?? '')
  const [description, setDescription] = useState(g.store?.description ?? '')
  const [category, setCategory] = useState(g.category ?? 'other')
  const [featured, setFeatured] = useState(Boolean(g.featured))
  const categories = useQuery({
    queryKey: ['categories'],
    queryFn: () => api.get('/v1/admin/categories').then((r) => r.categories ?? []),
    staleTime: Infinity,
  })
  const save = useMutation({
    mutationFn: () =>
      api.patch(`/v1/admin/games/${g.id}`, {
        tagline: tagline.trim(),
        description: description.trim(),
        category,
        featured,
      }),
    onSuccess: onSaved,
  })
  const dirty =
    tagline.trim() !== (g.tagline ?? '') ||
    description.trim() !== (g.store?.description ?? '') ||
    category !== (g.category ?? 'other') ||
    featured !== Boolean(g.featured)

  return (
    <Section title="Store copy" className={className}>
      <p className="text-ink-2 -mt-2 text-xs">The words a manifest does not carry. Ours to write.</p>
      <form
        className="grid gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (dirty) save.mutate()
        }}
      >
        <label className="grid gap-1 text-sm font-semibold">
          Tagline
          <input className="field font-normal" maxLength={160} value={tagline} onChange={(e) => setTagline(e.target.value)} />
          <span className="text-ink-3 text-xs font-normal">The line under the name. Required before it can be published.</span>
        </label>
        <label className="grid gap-1 text-sm font-semibold">
          Description
          <textarea
            className="field h-auto py-2 font-normal"
            rows={4}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="grid gap-1 text-sm font-semibold">
            Category
            <select className="field font-normal" value={category} onChange={(e) => setCategory(e.target.value)}>
              {(categories.data?.length ? categories.data : [category]).map((c) => (
                <option key={c} value={c}>
                  {categoryLabel[c] ?? c}
                </option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-2 self-end pb-2 text-sm font-semibold">
            <input type="checkbox" checked={featured} onChange={(e) => setFeatured(e.target.checked)} />
            Featured — leads the store
          </label>
        </div>
        {save.isError && <p className="text-bad text-sm">{save.error.message}</p>}
        <div className="flex justify-end">
          <button type="submit" className="btn btn-primary h-9 px-4 text-xs" disabled={save.isPending || !dirty}>
            {save.isPending ? 'Saving…' : 'Save store copy'}
          </button>
        </div>
      </form>
    </Section>
  )
}

// The pictures: the tile, and the page's screenshots. Each upload is its own
// call, so one that fails leaves the others saved.
function Pictures({ game: g, onChanged }) {
  const icon = useMutation({
    mutationFn: (file) => api.upload(`/v1/admin/games/${g.id}/icon`, file),
    onSuccess: onChanged,
  })
  const add = useMutation({
    mutationFn: (file) => api.upload(`/v1/admin/games/${g.id}/screenshots`, file, { method: 'POST' }),
    onSuccess: onChanged,
  })
  const remove = useMutation({
    mutationFn: (shot) => api.del(`/v1/admin/games/${g.id}/screenshots/${shot}`),
    onSuccess: onChanged,
  })
  const shots = g.store?.screenshots ?? []
  const error = (icon.error ?? add.error ?? remove.error)?.message

  return (
    <Section title="Pictures">
      <div className="flex items-center gap-4">
        <Tile src={g.icon} name={g.name} size="size-16" />
        <div className="min-w-0 flex-1">
          <div className="text-sm">{g.icon ? 'The tile picture is on the record.' : 'No icon yet. Required before it can be published.'}</div>
          <div className="text-ink-2 text-xs">PNG or JPEG, up to 2 MB.</div>
        </div>
        <label className="btn btn-secondary h-8 cursor-pointer px-3 text-xs">
          <Upload className="size-3.5" /> {icon.isPending ? 'Uploading…' : g.icon ? 'Replace icon' : 'Upload icon'}
          <input
            type="file"
            accept="image/png,image/jpeg"
            className="sr-only"
            disabled={icon.isPending}
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) icon.mutate(file)
              e.target.value = ''
            }}
          />
        </label>
      </div>
      <div>
        <div className="text-ink-3 text-xs font-semibold uppercase">Screenshots</div>
        <div className="mt-2 flex flex-wrap gap-2">
          {shots.map((s) => (
            <div key={s.id} className="relative">
              <a href={s.url} target="_blank" rel="noopener noreferrer">
                <img src={s.url} alt="" className="size-20 rounded-lg object-cover" />
              </a>
              <button
                type="button"
                aria-label="Remove screenshot"
                className="bg-bad absolute -top-1.5 -end-1.5 inline-flex size-5 items-center justify-center rounded-full text-white"
                disabled={remove.isPending}
                onClick={() => remove.mutate(s.id)}
              >
                <X className="size-3" />
              </button>
            </div>
          ))}
          {shots.length < 6 && (
            <label className="border-line-2 text-ink-2 hover:text-ink inline-flex size-20 cursor-pointer items-center justify-center rounded-lg border border-dashed text-xs">
              {add.isPending ? '…' : '+ add'}
              <input
                type="file"
                accept="image/png,image/jpeg"
                className="sr-only"
                disabled={add.isPending}
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  if (file) add.mutate(file)
                  e.target.value = ''
                }}
              />
            </label>
          )}
        </div>
        <p className="text-ink-3 mt-2 text-xs">Optional, up to six. PNG or JPEG, up to 2 MB each.</p>
      </div>
      {error && <p className="text-bad text-sm">{error}</p>}
    </Section>
  )
}

// The switches that are not "read the document again", kept apart from it
// because each one reaches every player at once. Each asks first.
function Actions({ game: g, busy, act }) {
  const [suspending, setSuspending] = useState(false)
  const [reason, setReason] = useState('')
  return (
    <Section title="Reaching every player">
      <Row icon={Sparkles} title={g.featured ? 'Featured' : 'Not featured'} text={g.featured ? 'Leads the store, whatever its tier.' : 'Players find it where its category puts it.'}>
        <button
          type="button"
          className="btn btn-secondary h-8 px-3 text-xs"
          disabled={Boolean(busy)}
          onClick={() => act('feature', g.featured ? 'Taken down from the top' : 'Featured', () => api.post(`/v1/admin/games/${g.id}/feature`, { featured: !g.featured }))}
        >
          {g.featured ? 'Take down' : 'Feature it'}
        </button>
      </Row>
      <Row icon={KeyRound} title="Client secret" text="Rolling it breaks the game until the studio has the new one.">
        <button
          type="button"
          className="btn btn-secondary h-8 px-3 text-xs"
          disabled={Boolean(busy)}
          onClick={() => {
            if (confirm('Roll the client secret? The old one stops at once. The new one is shown once, to you, and never again.'))
              act('secret', 'Client secret rolled', () => api.post(`/v1/admin/games/${g.id}/secret`))
          }}
        >
          Roll
        </button>
      </Row>
      {g.listing !== 'suspended' && (
        <Row icon={Ban} title="Suspend" text="Out of every store and shelf, and its tokens stop the same moment." tone="text-bad">
          <button type="button" className="btn btn-secondary text-bad h-8 px-3 text-xs" disabled={Boolean(busy)} onClick={() => setSuspending(true)}>
            Suspend
          </button>
        </Row>
      )}
      <Dialog
        open={suspending}
        onClose={() => setSuspending(false)}
        title={`Suspend ${g.name}?`}
        description="Every player loses it at once and its tokens stop. Shelves are kept, so publishing brings it back."
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setSuspending(false)}>
              Cancel
            </button>
            <button
              type="button"
              className="btn btn-primary"
              disabled={Boolean(busy)}
              onClick={() => {
                setSuspending(false)
                act('suspend', 'Suspended', () => api.post(`/v1/admin/games/${g.id}/suspend`, { reason: reason.trim() }))
              }}
            >
              Yes, suspend it
            </button>
          </>
        }
      >
        <label className="grid gap-1 text-sm font-semibold">
          Reason, for the log
          <input className="field font-normal" maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} />
        </label>
      </Dialog>
    </Section>
  )
}

function Row({ icon: Icon, title, text, tone = 'text-ink-2', children }) {
  return (
    <div className="flex items-center gap-3">
      <span className={`bg-surface-2 inline-flex size-9 shrink-0 items-center justify-center rounded-md ${tone}`}>
        <Icon className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-semibold">{title}</div>
        <div className="text-ink-2 text-xs">{text}</div>
      </div>
      {children}
    </div>
  )
}
