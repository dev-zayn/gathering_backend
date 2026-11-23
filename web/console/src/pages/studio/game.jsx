import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, KeyRound, Plus, RefreshCw, Send, Trash2, Undo2, Upload, X } from 'lucide-react'

import { api } from '../../api.js'
import Dialog from '../../components/dialog.jsx'
import { BuildBadge, Changes, CodeChip, ManifestFacts, ReviewWords, RoomFacts, Steps } from '../../components/build-bits.jsx'
import { ListingBadge, Note, SecretOnce, Tile } from '../../components/game-bits.jsx'
import { inviteLive, inviteState } from '../../lib/builds.js'
import { categoryLabel, when } from '../../lib/games.js'

// One of the publisher's games (docs/PUBLISHERS.md): what players have now,
// the version on its way with every step it can take from where it stands,
// the testers and their codes, and every version before.
export default function StudioGame() {
  const { id } = useParams()
  const queries = useQueryClient()
  const game = useQuery({ queryKey: ['studio', 'game', id], queryFn: () => api.get(`/v1/publisher/games/${id}`) })
  const [note, setNote] = useState(null)
  const [secret, setSecret] = useState(null)

  function changed() {
    queries.invalidateQueries({ queryKey: ['studio'] })
  }

  // One step at a time: it says how it went, and the page stays busy until
  // it shows the result.
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
        <Link to="/" className="btn btn-secondary mt-4">
          <ArrowLeft className="size-4" /> My games
        </Link>
      </div>
    )
  const g = game.data
  const b = g.build
  const live = g.listing !== 'unpublished'

  return (
    <div className="grid gap-5">
      <div>
        <Link to="/" className="text-ink-2 hover:text-ink inline-flex items-center gap-1 text-xs font-semibold">
          <ArrowLeft className="size-3.5" /> My games
        </Link>
        <div className="mt-2 flex flex-wrap items-center gap-4">
          <Tile src={g.icon || b?.store?.icon} name={g.name} size="size-16" />
          <div className="min-w-0">
            <h1 className="text-2xl font-extrabold">{g.name}</h1>
            <div className="text-ink-2 mt-0.5 flex flex-wrap items-center gap-2 text-sm">
              <span className="font-mono text-xs" dir="ltr">
                {g.id}
              </span>
              <ListingBadge listing={g.listing} />
              {g.version && <span className="font-mono text-xs">live {g.version}</span>}
              <span className="text-xs">
                on {g.players} shelves · {g.testers} testers
              </span>
            </div>
          </div>
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
      {secret && <SecretOnce secret={secret} hint="Put it on your game’s server now: it cannot be read back." />}
      {g.listing === 'suspended' && (
        <Note tone="bad">
          The گعدة team suspended this game: it is out of every store, testers included, and nothing can be
          published. Get in touch with them.
        </Note>
      )}

      {b ? <OpenBuild game={g} build={b} live={live} busy={busy} act={act} onChanged={changed} /> : <NextVersion game={g} onMade={changed} />}

      <Testers game={g} />

      <div className="grid gap-5 lg:grid-cols-3">
        <History game={g} className="lg:col-span-2" />
        <Section title="Client secret">
          <p className="text-ink-2 text-sm">
            Your server proves it is this game with it. Rolling it stops the old one at once.
          </p>
          <div>
            <button
              type="button"
              className="btn btn-secondary h-8 px-3 text-xs"
              disabled={Boolean(busy)}
              onClick={() => {
                if (confirm('Roll the client secret? The old one stops at once, and the new one is shown once.'))
                  act('secret', 'Client secret rolled', () => api.post(`/v1/publisher/games/${g.id}/secret`))
              }}
            >
              <KeyRound className="size-3.5" /> Roll the secret
            </button>
          </div>
        </Section>
      </div>
    </div>
  )
}

function Section({ title, children, className = '', aside = null }) {
  return (
    <section className={`card p-4 ${className}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-extrabold">{title}</h2>
        {aside}
      </div>
      <div className="mt-3 grid gap-3">{children}</div>
    </section>
  )
}

// The version on its way: where it stands, the step it can take next, what
// its document says, and the store copy it will carry.
function OpenBuild({ game: g, build: b, live, busy, act, onChanged }) {
  const frozen = b.status === 'in_review'
  const missing = [!b.store.tagline && 'a tagline', !b.store.icon && 'an icon'].filter(Boolean)
  const path = `/v1/publisher/builds/${b.id}`

  return (
    <section className="card grid gap-4 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-lg font-extrabold">Version {b.version}</h2>
            <BuildBadge status={b.status} />
          </div>
          <Steps status={b.status} />
        </div>
        <div className="flex flex-wrap gap-2">
          {(b.status === 'testing' || b.status === 'rejected') && (
            <button
              type="button"
              className="btn btn-primary"
              disabled={Boolean(busy) || missing.length > 0}
              title={missing.length ? `Needs ${missing.join(' and ')} first` : undefined}
              onClick={() => act('submit', 'Sent to review. Testers keep playing it meanwhile.', () => api.post(`${path}/submit`))}
            >
              <Send className="size-4" /> Send to review
            </button>
          )}
          {b.status === 'in_review' && (
            <button
              type="button"
              className="btn btn-secondary"
              disabled={Boolean(busy)}
              onClick={() => act('cancel', 'Taken back from review', () => api.post(`${path}/cancel`))}
            >
              <Undo2 className="size-4" /> Take it back
            </button>
          )}
          {b.status === 'approved' && (
            <button
              type="button"
              className="btn btn-primary"
              disabled={Boolean(busy) || g.listing === 'suspended'}
              onClick={() => {
                if (
                  confirm(
                    live
                      ? `Publish version ${b.version}? Every player gets it from their next open; rooms already open keep theirs.`
                      : `Publish ${b.name}? It appears in every player’s store the moment you confirm.`,
                  )
                )
                  act('publish', `Version ${b.version} is live`, () => api.post(`${path}/publish`))
              }}
            >
              <Upload className="size-4" /> Publish
            </button>
          )}
          {b.status !== 'in_review' && (
            <button
              type="button"
              className="btn btn-secondary text-bad"
              disabled={Boolean(busy)}
              onClick={() => {
                if (confirm(`Discard version ${b.version}? Testers get ${live ? 'the live game' : 'nothing'} back until your next version.`))
                  act('withdraw', `Version ${b.version} discarded`, () => api.post(`${path}/withdraw`))
              }}
            >
              <Trash2 className="size-4" /> Discard
            </button>
          )}
        </div>
      </div>

      <ReviewWords build={b} />
      {frozen && (
        <Note tone="wait">
          Waiting for a reviewer since {b.submitted_at ? b.submitted_at.slice(0, 16).replace('T', ' ') : 'now'}. Nothing
          can change while it is in review; take it back to change it. Testers keep playing it.
        </Note>
      )}
      {!frozen && missing.length > 0 && (
        <Note tone="wait">Before review it needs {missing.join(' and ')}: write them below.</Note>
      )}
      {b.status === 'approved' && (
        <p className="text-ink-3 text-xs">Changing the store copy, the pictures or the manifest takes the approval back.</p>
      )}

      <div className="grid gap-5 lg:grid-cols-3">
        <div className="grid content-start gap-3">
          <h3 className="label">What its manifest says</h3>
          <ManifestFacts build={b} />
          <Reread build={b} disabled={frozen || Boolean(busy)} onDone={onChanged} />
        </div>
        <div className="grid content-start gap-3">
          <h3 className="label">The room</h3>
          <RoomFacts room={b.room} />
        </div>
        <div className="grid content-start gap-3">
          <h3 className="label">What publishing changes</h3>
          <Changes build={b} live={live} />
        </div>
      </div>

      <div className="border-line grid gap-5 border-t pt-4 lg:grid-cols-2">
        <StoreCopy key={copyKey(b)} build={b} disabled={frozen} onSaved={onChanged} />
        <Pictures build={b} disabled={frozen} onChanged={onChanged} />
      </div>
    </section>
  )
}

const copyKey = (b) => [b.id, b.store.tagline, b.store.description, b.store.category, b.notes].join('|')

// Reading the document again, from the same address or a new one.
function Reread({ build: b, disabled, onDone }) {
  const [open, setOpen] = useState(false)
  const [url, setUrl] = useState(b.manifest_url)
  const read = useMutation({
    mutationFn: (to) => api.post(`/v1/publisher/builds/${b.id}/refresh`, to ? { url: to } : {}),
    onSuccess: () => {
      setOpen(false)
      onDone()
    },
  })
  return (
    <div className="flex flex-wrap gap-2">
      <button type="button" className="btn btn-secondary h-8 px-3 text-xs" disabled={disabled || read.isPending} onClick={() => read.mutate('')}>
        <RefreshCw className="size-3.5" /> {read.isPending && !open ? 'Reading…' : 'Read it again'}
      </button>
      <button
        type="button"
        className="btn btn-secondary h-8 px-3 text-xs"
        disabled={disabled}
        onClick={() => {
          setUrl(b.manifest_url)
          read.reset()
          setOpen(true)
        }}
      >
        Another address
      </button>
      {read.isError && !open && <p className="text-bad w-full text-xs">{read.error.message}</p>}
      <Dialog
        open={open}
        onClose={() => !read.isPending && setOpen(false)}
        title="Read this version from another address"
        description="The document there must name this game, at a version other than the live one."
        footer={
          <>
            <button type="button" className="btn btn-secondary" onClick={() => setOpen(false)} disabled={read.isPending}>
              Cancel
            </button>
            <button type="submit" form="reread-url" className="btn btn-primary" disabled={read.isPending || !url.trim()}>
              {read.isPending ? 'Reading…' : 'Read it'}
            </button>
          </>
        }
      >
        <form
          id="reread-url"
          className="grid gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            read.mutate(url.trim())
          }}
        >
          <input
            className="field font-mono text-xs"
            type="url"
            dir="ltr"
            autoComplete="off"
            spellCheck={false}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
          {read.isError && <p className="text-bad text-sm">{read.error.message}</p>}
        </form>
      </Dialog>
    </div>
  )
}

// The words this version carries into the store, and the notes testers and
// the reviewer read.
function StoreCopy({ build: b, disabled, onSaved }) {
  const [tagline, setTagline] = useState(b.store.tagline ?? '')
  const [description, setDescription] = useState(b.store.description ?? '')
  const [category, setCategory] = useState(b.store.category ?? 'other')
  const [notes, setNotes] = useState(b.notes ?? '')
  const categories = useQuery({
    queryKey: ['studio', 'categories'],
    queryFn: () => api.get('/v1/publisher/categories').then((r) => r.categories ?? []),
    staleTime: Infinity,
  })
  const save = useMutation({
    mutationFn: () =>
      api.patch(`/v1/publisher/builds/${b.id}`, {
        tagline: tagline.trim(),
        description: description.trim(),
        category,
        notes: notes.trim(),
      }),
    onSuccess: onSaved,
  })
  const dirty =
    tagline.trim() !== (b.store.tagline ?? '') ||
    description.trim() !== (b.store.description ?? '') ||
    category !== (b.store.category ?? 'other') ||
    notes.trim() !== (b.notes ?? '')

  return (
    <form
      className="grid content-start gap-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (dirty) save.mutate()
      }}
    >
      <h3 className="label">Store copy</h3>
      <fieldset disabled={disabled} className="grid gap-3">
        <label className="grid gap-1 text-sm font-semibold">
          Tagline
          <input className="field font-normal" maxLength={160} value={tagline} onChange={(e) => setTagline(e.target.value)} />
          <span className="text-ink-3 text-xs font-normal">The line under the name in the store. Needed for review.</span>
        </label>
        <label className="grid gap-1 text-sm font-semibold">
          Description
          <textarea
            className="field h-auto py-2 font-normal"
            rows={4}
            maxLength={4000}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
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
        <label className="grid gap-1 text-sm font-semibold">
          Notes for testers and the reviewer
          <textarea
            className="field h-auto py-2 font-normal"
            rows={3}
            maxLength={2000}
            placeholder="What is new in this version, and what to try."
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
          />
          <span className="text-ink-3 text-xs font-normal">Testers see these on the game’s page in the app.</span>
        </label>
      </fieldset>
      {save.isError && <p className="text-bad text-sm">{save.error.message}</p>}
      <div className="flex justify-end">
        <button type="submit" className="btn btn-primary h-9 px-4 text-xs" disabled={disabled || save.isPending || !dirty}>
          {save.isPending ? 'Saving…' : 'Save'}
        </button>
      </div>
    </form>
  )
}

// The tile and the screenshots this version carries. It starts with the live
// game's; uploading replaces them for this version alone.
function Pictures({ build: b, disabled, onChanged }) {
  const base = `/v1/publisher/builds/${b.id}`
  const icon = useMutation({ mutationFn: (file) => api.upload(`${base}/icon`, file), onSuccess: onChanged })
  const add = useMutation({
    mutationFn: (file) => api.upload(`${base}/screenshots`, file, { method: 'POST' }),
    onSuccess: onChanged,
  })
  const remove = useMutation({ mutationFn: (shot) => api.del(`${base}/screenshots/${shot}`), onSuccess: onChanged })
  const shots = b.store.screenshots ?? []
  const error = (icon.error ?? add.error ?? remove.error)?.message

  return (
    <div className="grid content-start gap-3">
      <h3 className="label">Pictures</h3>
      <div className="flex items-center gap-4">
        <Tile src={b.store.icon} name={b.name} size="size-16" />
        <div className="min-w-0 flex-1">
          <div className="text-sm">{b.store.icon ? 'The tile players will see.' : 'No icon yet. Needed for review.'}</div>
          <div className="text-ink-2 text-xs">PNG or JPEG, square, up to 2 MB.</div>
        </div>
        <label className={`btn btn-secondary h-8 px-3 text-xs ${disabled ? 'pointer-events-none opacity-50' : 'cursor-pointer'}`}>
          <Upload className="size-3.5" /> {icon.isPending ? 'Uploading…' : b.store.icon ? 'Replace' : 'Upload'}
          <input
            type="file"
            accept="image/png,image/jpeg"
            className="sr-only"
            disabled={disabled || icon.isPending}
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) icon.mutate(file)
              e.target.value = ''
            }}
          />
        </label>
      </div>
      <div>
        <div className="text-ink-2 text-xs">Screenshots, up to six</div>
        <div className="mt-2 flex flex-wrap gap-2">
          {shots.map((s) => (
            <div key={s.id} className="relative">
              <a href={s.url} target="_blank" rel="noopener noreferrer">
                <img src={s.url} alt="" className="size-20 rounded-lg object-cover" />
              </a>
              {!disabled && (
                <button
                  type="button"
                  aria-label="Remove screenshot"
                  className="bg-bad absolute -end-1.5 -top-1.5 inline-flex size-5 items-center justify-center rounded-full text-white"
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(s.id)}
                >
                  <X className="size-3" />
                </button>
              )}
            </div>
          ))}
          {shots.length < 6 && !disabled && (
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
      </div>
      {error && <p className="text-bad text-sm">{error}</p>}
    </div>
  )
}

// No version on its way: the next one starts from a manifest, read from the
// live one's address unless told otherwise.
function NextVersion({ game: g, onMade }) {
  const [url, setUrl] = useState(g.manifest_url ?? '')
  const make = useMutation({
    mutationFn: () => api.post(`/v1/publisher/games/${g.id}/builds`, { url: url.trim() }),
    onSuccess: onMade,
  })
  return (
    <Section title="The next version">
      <p className="text-ink-2 text-sm">
        Bump the version in your manifest, then read it here. Testers get it at once; players keep the live
        game until it is reviewed and you publish it.
      </p>
      <form
        className="flex flex-col gap-2 sm:flex-row"
        onSubmit={(e) => {
          e.preventDefault()
          make.mutate()
        }}
      >
        <input
          className="field min-w-0 flex-1 font-mono text-xs"
          type="url"
          dir="ltr"
          autoComplete="off"
          spellCheck={false}
          aria-label="Manifest URL of the next version"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
        <button type="submit" className="btn btn-primary" disabled={make.isPending || g.listing === 'suspended'}>
          <Plus className="size-4" /> {make.isPending ? 'Reading…' : 'Start it'}
        </button>
      </form>
      <p className="text-ink-3 text-xs">
        Players load whatever your live entry serves. Host a test version at an address of its own (say /beta/) so
        testers try it without anybody else getting it.
      </p>
      {make.isError && <p className="text-bad text-sm">{make.error.message}</p>}
    </Section>
  )
}

// The testers and the codes that made them.
function Testers({ game: g }) {
  const queries = useQueryClient()
  const list = useQuery({
    queryKey: ['studio', 'testers', g.id],
    queryFn: () => api.get(`/v1/publisher/games/${g.id}/testers`),
  })
  const changed = () => queries.invalidateQueries({ queryKey: ['studio'] })
  const revoke = useMutation({ mutationFn: (code) => api.del(`/v1/publisher/games/${g.id}/invites/${code}`), onSuccess: changed })
  const remove = useMutation({ mutationFn: (player) => api.del(`/v1/publisher/games/${g.id}/testers/${player}`), onSuccess: changed })
  const invites = list.data?.invites ?? []
  const testers = list.data?.testers ?? []
  const error = (list.error ?? revoke.error ?? remove.error)?.message

  return (
    <Section title={`Testers · ${testers.length}`} aside={<NewCode game={g} onMade={changed} />}>
      <p className="text-ink-2 text-sm">
        A tester types a code in the app (Me → Beta games) and plays the version on its way instead of the live
        one, in their own rooms too. Only testers can join a tester’s room.
      </p>
      {error && <p className="text-bad text-sm">{error}</p>}
      <div className="grid gap-5 lg:grid-cols-2">
        <div>
          <h3 className="label">Codes</h3>
          {invites.length === 0 ? (
            <p className="text-ink-3 mt-2 text-sm">No code yet. Make one and share it.</p>
          ) : (
            <ul className="mt-2 grid gap-2">
              {invites.map((i) => {
                const ok = inviteLive(i)
                return (
                  <li key={i.code} className="border-line flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2">
                    <div className="min-w-0">
                      <CodeChip code={i.code} muted={!ok} />
                      <div className="text-ink-2 mt-0.5 text-xs">
                        {i.label || 'No label'} · {i.uses}
                        {i.max_uses ? ` of ${i.max_uses}` : ''} joined ·{' '}
                        {ok ? `until ${i.expires_at.slice(0, 10)}` : inviteState(i)}
                      </div>
                    </div>
                    {ok && (
                      <button
                        type="button"
                        className="btn btn-secondary h-7 px-2 text-xs"
                        disabled={revoke.isPending}
                        onClick={() => {
                          if (confirm('Stop this code? Who already joined with it stays a tester.')) revoke.mutate(i.code)
                        }}
                      >
                        Stop
                      </button>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
        </div>
        <div>
          <h3 className="label">Who tests it</h3>
          {testers.length === 0 ? (
            <p className="text-ink-3 mt-2 text-sm">Nobody yet.</p>
          ) : (
            <ul className="mt-2 grid gap-2">
              {testers.map((t) => (
                <li key={t.id} className="border-line flex items-center justify-between gap-2 rounded-lg border px-3 py-2">
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold">{t.nickname}</div>
                    <div className="text-ink-2 truncate text-xs" dir="ltr">
                      @{t.handle} · {when(t.joined_at)}
                    </div>
                  </div>
                  <button
                    type="button"
                    className="btn btn-secondary h-7 px-2 text-xs"
                    disabled={remove.isPending}
                    onClick={() => {
                      if (confirm(`Take ${t.nickname} off the testers? They get the live game from now on.`)) remove.mutate(t.id)
                    }}
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </Section>
  )
}

// A new code: a label to know it by, and how far it reaches.
function NewCode({ game: g, onMade }) {
  const [open, setOpen] = useState(false)
  const [label, setLabel] = useState('')
  const [uses, setUses] = useState('')
  const [days, setDays] = useState('30')
  const make = useMutation({
    mutationFn: () =>
      api.post(`/v1/publisher/games/${g.id}/invites`, {
        label: label.trim(),
        ...(uses ? { max_uses: Number(uses) } : {}),
        days: Number(days),
      }),
    onSuccess: onMade,
  })
  const made = make.data

  function close() {
    setOpen(false)
    setLabel('')
    setUses('')
    setDays('30')
    make.reset()
  }

  return (
    <>
      <button type="button" className="btn btn-secondary h-8 px-3 text-xs" onClick={() => setOpen(true)}>
        <Plus className="size-3.5" /> New code
      </button>
      <Dialog
        open={open}
        onClose={close}
        title={made ? 'Share this code' : 'A new tester’s code'}
        description={
          made
            ? 'Testers open the app, then Me → Beta games, and type it there.'
            : 'Whoever types it in the app tests this game from then on.'
        }
        footer={
          made ? (
            <button type="button" className="btn btn-primary" onClick={close}>
              Done
            </button>
          ) : (
            <>
              <button type="button" className="btn btn-secondary" onClick={close} disabled={make.isPending}>
                Cancel
              </button>
              <button type="submit" form="new-code" className="btn btn-primary" disabled={make.isPending}>
                {make.isPending ? 'Making…' : 'Make it'}
              </button>
            </>
          )
        }
      >
        {made ? (
          <div className="bg-surface-2 grid place-items-center rounded-lg py-6">
            <CodeChip code={made.code} />
          </div>
        ) : (
          <form
            id="new-code"
            className="grid gap-3"
            onSubmit={(e) => {
              e.preventDefault()
              make.mutate()
            }}
          >
            <label className="grid gap-1 text-sm font-semibold">
              Label
              <input
                className="field font-normal"
                maxLength={60}
                placeholder="Friends, the QA team…"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
              />
            </label>
            <div className="grid grid-cols-2 gap-3">
              <label className="grid gap-1 text-sm font-semibold">
                How many may join
                <input
                  className="field font-normal"
                  type="number"
                  min={1}
                  max={1000}
                  placeholder="No limit"
                  value={uses}
                  onChange={(e) => setUses(e.target.value)}
                />
              </label>
              <label className="grid gap-1 text-sm font-semibold">
                Days it works
                <input
                  className="field font-normal"
                  type="number"
                  min={1}
                  max={90}
                  required
                  value={days}
                  onChange={(e) => setDays(e.target.value)}
                />
              </label>
            </div>
            {make.isError && <p className="text-bad text-sm">{make.error.message}</p>}
          </form>
        )}
      </Dialog>
    </>
  )
}

// Every version, newest first, with the reviewer's words on each.
function History({ game: g, className = '' }) {
  return (
    <Section title="Versions" className={className}>
      {g.builds.length === 0 ? (
        <p className="text-ink-3 text-sm">None yet.</p>
      ) : (
        <ul className="grid gap-2">
          {g.builds.map((b) => (
            <li key={b.id} className="border-line grid gap-1 rounded-lg border px-3 py-2">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="flex items-center gap-2">
                  <span className="font-mono text-sm font-semibold">{b.version}</span>
                  <BuildBadge status={b.status} />
                </span>
                <span className="text-ink-2 text-xs">
                  {b.published_at
                    ? `published ${when(b.published_at)}`
                    : b.reviewed_at
                      ? `reviewed ${when(b.reviewed_at)}`
                      : b.submitted_at
                        ? `sent ${when(b.submitted_at)}`
                        : `started ${when(b.created_at)}`}
                </span>
              </div>
              {b.note && <p className="text-ink-2 text-xs whitespace-pre-wrap">“{b.note}”</p>}
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}
