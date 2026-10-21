import { Fragment } from 'react'
import { Link } from 'react-router-dom'
import { ArrowLeftRightIcon, ArrowRightIcon, CompassIcon } from 'lucide-react'

import { Button } from '@/components/mdx'
import { ICONS, PAGES, known } from '@/pages.js'
import { cn } from '@/lib/utils'

// The larger pieces a page can place: where to go next, the launch drawn,
// and the page that is not there.

// The landing: what this is, in one breath, beside the document it all
// hangs on. The manifest is real and short, so it is shown rather than
// described.
export function Hero() {
  return (
    <section className="skeu-panel-2 not-prose relative mb-12 grid items-center gap-8 overflow-hidden rounded-2xl p-8 xl:grid-cols-[1.1fr_1fr] xl:p-10">
      {/* A glow of the accent in the corner. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 [mask-image:linear-gradient(180deg,black_0%,rgba(0,0,0,0.35)_45%,transparent_75%)]"
      >
        <div className="absolute inset-0 bg-[radial-gradient(120%_70%_at_100%_0%,var(--color-accent-soft),transparent_65%)]" />
      </div>
      <div className="relative">
        <h1 className="text-[40px] leading-[1.08] font-extrabold text-ink">
          Build a game people play together in one room
        </h1>
        <p className="mt-4 max-w-xl text-[16.5px] leading-relaxed text-ink-2">
          Gathering is where friends open a room and play. Your game appears in it as a tile:
          they tap it, the room is already made, the players already in it, every message
          already relayed. No accounts, no sockets, no lobby to write.
        </p>
        <div className="mt-6 flex flex-wrap gap-3">
          <Button href="/quickstart" arrow="right">
            Start in fifteen minutes
          </Button>
          <Button href="/bridge" variant="outline" icon={ArrowLeftRightIcon}>
            Explore the bridge
          </Button>
        </div>
      </div>
      <div className="code-panel skeu-dark relative overflow-hidden rounded-xl">
        <div className="flex h-10 items-center gap-2 border-b border-white/10 px-4 text-xs font-bold text-white">
          <span className="text-id font-normal text-white/60">/.well-known/gathering-game.json</span>
          <span className="ms-auto skeu-chip-solid inline-flex h-5 items-center rounded-full px-2 text-[10px]">
            the whole of it
          </span>
        </div>
        <pre className="overflow-x-auto p-4 text-[12.5px] leading-[1.7] text-[#ede6dd]">
          <Manifest />
        </pre>
      </div>
    </section>
  )
}

// The example manifest, painted with the highlighter's colours by hand: this
// one is JSX, not a fence, so the build's highlighter never sees it. No real
// company in it — the reader puts their own name where the placeholders are.
const MANIFEST = [
  [['p', '{']],
  [
    ['k', '"id"'],
    ['p', ': '],
    ['s', '"yourstudio.game"'],
    ['p', ','],
  ],
  [
    ['k', '"name"'],
    ['p', ': '],
    ['s', '"اسم لعبتك"'],
    ['p', ','],
  ],
  [
    ['k', '"publisher"'],
    ['p', ': { '],
    ['k', '"tier"'],
    ['p', ': '],
    ['s', '"partner"'],
    ['p', ', '],
    ['k', '"name"'],
    ['p', ': '],
    ['s', '"اسم استوديوك"'],
    ['p', ' },'],
  ],
  [
    ['k', '"version"'],
    ['p', ': '],
    ['s', '"1.0.0"'],
    ['p', ','],
  ],
  [
    ['k', '"mode"'],
    ['p', ': '],
    ['s', '"embedded"'],
    ['p', ','],
  ],
  [
    ['k', '"entry"'],
    ['p', ': '],
    ['s', '"https://games.yourstudio.example/"'],
    ['p', ','],
  ],
  [
    ['k', '"orientation"'],
    ['p', ': '],
    ['s', '"portrait"'],
    ['p', ','],
  ],
  [
    ['k', '"room"'],
    ['p', ': { '],
    ['k', '"players"'],
    ['p', ': { '],
    ['k', '"min"'],
    ['p', ': 2, '],
    ['k', '"max"'],
    ['p', ': 8 }, '],
    ['k', '"pace"'],
    ['p', ': '],
    ['s', '"realtime"'],
    ['p', ' }'],
  ],
  [['p', '}']],
]

const PAINT = {
  k: 'var(--shiki-token-constant)',
  s: 'var(--shiki-token-string)',
  p: 'var(--shiki-token-punctuation)',
}

function Manifest() {
  return (
    <code>
      {MANIFEST.map((line, i) => (
        <span key={i} className="block">
          {i > 0 && i < MANIFEST.length - 1 ? '  ' : ''}
          {line.map(([kind, text], j) => (
            <span key={j} style={{ color: PAINT[kind] }}>
              {text}
            </span>
          ))}
        </span>
      ))}
    </code>
  )
}

// The work, as the brand's stepper: four steps, each a page. The reader sees
// the whole road before the first turn.
const ROAD = [
  {
    href: '/manifest',
    title: 'Publish a manifest',
    text: 'One JSON file at a fixed address on your origin. Everything wrong with it comes back at once.',
  },
  {
    href: '/listing',
    title: 'Send us the address',
    text: 'We fetch it, check it, and hand you a client secret — shown once, stored only as a hash.',
  },
  {
    href: '/auth',
    title: 'Handle the launch',
    text: 'A player taps your tile and arrives in a room. Your page talks to it through window.gathering.',
  },
  {
    href: '/bridge',
    title: 'Ask the bridge',
    text: 'The room relays your messages, keeps the shared state and tells you who joined and left.',
  },
]

export function Journey() {
  return (
    <section className="not-prose skeu-panel-2 my-12 rounded-2xl p-5 sm:p-6">
      <ol className="hidden items-center gap-2 text-xs font-semibold sm:flex">
        {ROAD.map((step, i) => (
          <Fragment key={step.href}>
            <li className="flex items-center gap-2">
              <span
                className={cn(
                  'inline-flex size-8 items-center justify-center rounded-full tabular-nums',
                  i === 0 ? 'skeu-tile' : 'skeu-tile-soft',
                )}
              >
                {i + 1}
              </span>
              {step.title}
            </li>
            {i < ROAD.length - 1 && (
              <li
                className="skeu-track h-1.5 flex-1 overflow-hidden rounded-full"
                aria-hidden="true"
              >
                <div className="skeu-fill h-full" style={{ width: i === 0 ? '100%' : '0%' }} />
              </li>
            )}
          </Fragment>
        ))}
      </ol>
      <div className="mt-5 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {ROAD.map((step, i) => (
          <Link
            key={step.href}
            to={step.href}
            className="skeu-panel skeu-panel-interactive group flex flex-col gap-2 rounded-xl p-4"
          >
            <span className="text-label">Step {i + 1}</span>
            <span className="text-sm font-bold text-ink">{step.title}</span>
            <span className="flex-1 text-[13px] leading-relaxed text-ink-2">{step.text}</span>
            <span className="inline-flex items-center gap-1 text-[13px] font-semibold text-accent-ink">
              Open
              <ArrowRightIcon
                className="size-3.5 transition group-hover:translate-x-0.5"
                aria-hidden="true"
              />
            </span>
          </Link>
        ))}
      </div>
    </section>
  )
}

// What the player sees, drawn: the tile, the game open, and a line from it
// in their inbox. Three screens at a phone's width, since that is the only
// width that exists.
const SCREENS = [
  {
    title: 'Your tile',
    text: 'In the store, with your name, your icon and one line about what it does.',
  },
  { title: 'Your game', text: 'Open, in a room, with the players already in it. Nothing to type.' },
  {
    title: 'Their inbox',
    text: 'A line from you lands here, with your name on it, whenever you send one.',
  },
]

export function Player() {
  return (
    <section className="not-prose my-12">
      <h2 id="what-a-player-sees" className="text-[21px] font-bold text-ink">
        What a player sees
      </h2>
      <div className="mt-5 grid gap-4 sm:grid-cols-3">
        {SCREENS.map((s, i) => (
          <figure key={s.title} className="flex flex-col gap-3">
            <div className="skeu-panel-2 mx-auto w-full max-w-[220px] rounded-[1.6rem] p-1.5">
              <div className="skeu-well flex h-[300px] flex-col gap-2 overflow-hidden rounded-[1.2rem] p-3">
                <div className="flex items-center justify-between px-1 text-[9px] font-semibold text-ink-3">
                  <span>9:41</span>
                  <span className="h-2.5 w-12 rounded-full bg-ink" />
                  <span>100%</span>
                </div>
                {i === 0 && (
                  <>
                    <div className="skeu-input h-6 rounded-md" />
                    <div className="grid grid-cols-2 gap-2">
                      {[0, 1, 2, 3].map((n) => (
                        <div
                          key={n}
                          className={cn(
                            'skeu-panel flex flex-col items-center gap-1.5 rounded-lg p-2',
                            n === 1 && 'ring-2 ring-accent',
                          )}
                        >
                          <span
                            className={cn(
                              'size-7 rounded-md',
                              n === 1 ? 'skeu-tile' : 'skeu-tile-ceramic',
                            )}
                          />
                          <span className="h-1.5 w-10 rounded-full bg-line-2" />
                        </div>
                      ))}
                    </div>
                  </>
                )}
                {i === 1 && (
                  <div className="skeu-panel flex flex-1 flex-col gap-2 rounded-lg p-3">
                    <div className="flex items-center gap-2">
                      <span className="skeu-tile-ceramic size-6 rounded-full" />
                      <span className="h-2 w-16 rounded-full bg-ink/70" />
                    </div>
                    <span className="mt-2 h-1.5 w-full rounded-full bg-line-2" />
                    <span className="h-1.5 w-5/6 rounded-full bg-line-2" />
                    <span className="h-1.5 w-2/3 rounded-full bg-line-2" />
                    <span className="skeu-well-accent mt-auto h-10 rounded-md" />
                  </div>
                )}
                {i === 2 && (
                  <div className="flex flex-col gap-2">
                    <span className="h-2 w-14 rounded-full bg-ink/70" />
                    {[0, 1, 2, 3].map((n) => (
                      <div
                        key={n}
                        className={cn(
                          'skeu-panel flex items-center gap-2 rounded-lg p-2',
                          n === 0 && 'ring-2 ring-accent',
                        )}
                      >
                        <span
                          className={cn(
                            'size-6 shrink-0 rounded-md',
                            n === 0 ? 'skeu-tile' : 'skeu-tile-ceramic',
                          )}
                        />
                        <span className="flex min-w-0 flex-1 flex-col gap-1">
                          <span className="h-1.5 w-16 rounded-full bg-ink/70" />
                          <span className="h-1.5 w-full rounded-full bg-line-2" />
                        </span>
                        {n === 0 && <span className="size-2 shrink-0 rounded-full bg-accent" />}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
            <figcaption className="text-center">
              <span className="block text-sm font-bold text-ink">{s.title}</span>
              <span className="mt-0.5 block text-[13px] leading-relaxed text-ink-2">{s.text}</span>
            </figcaption>
          </figure>
        ))}
      </div>
    </section>
  )
}

// Where to next: four cards, each wearing its page's mark over the brand tile,
// the way the hero does. Hovering lights the tile solid and drifts the pattern.
export function Guides({ guides, title, id }) {
  return (
    <section className="not-prose my-14">
      <h2 id={id} className="text-[21px] font-bold text-ink">
        {title}
      </h2>
      <div className="mt-5 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {guides.map((g) => {
          // A page in this site wears its own tile. One in the other section is
          // not in this bundle, so the guide names the icon itself.
          const Icon = PAGES.find((p) => p.href === g.href)?.Icon ?? ICONS[g.icon] ?? CompassIcon
          return (
            <Card
              key={g.href}
              href={g.href}
              className="skeu-panel skeu-panel-interactive guide-card group relative flex flex-col gap-3 overflow-hidden rounded-xl p-4"
            >
              <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-0 opacity-60 transition-opacity duration-300 [mask-image:linear-gradient(180deg,black_0%,rgba(0,0,0,0.35)_50%,transparent_85%)] group-hover:opacity-100"
              >
                <div className="absolute inset-0 bg-[radial-gradient(120%_80%_at_100%_0%,var(--color-accent-soft),transparent_65%)] transition-transform duration-500 group-hover:scale-125" />
                <div
                  className="absolute -inset-4 bg-repeat transition-transform duration-700 ease-out group-hover:-translate-x-3 group-hover:-translate-y-3"
                  style={{
                    backgroundImage: 'url(/brand/pattern.svg)',
                    backgroundSize: '120px 120px',
                    filter:
                      'invert(45%) sepia(80%) saturate(500%) hue-rotate(350deg) brightness(95%)',
                    opacity: 0.14,
                  }}
                />
              </div>
              <span className="guide-tile skeu-tile-soft relative inline-flex size-10 items-center justify-center rounded-lg">
                {Icon && <Icon className="size-5" aria-hidden="true" />}
              </span>
              <h3 className="relative text-sm font-bold text-ink transition-colors group-hover:text-accent-ink">
                {g.name}
              </h3>
              <p className="relative flex-1 text-[13px] leading-relaxed text-ink-2">
                {g.description}
              </p>
              <span className="relative inline-flex items-center gap-1 text-[13px] font-semibold text-accent-ink">
                Read more
                <ArrowRightIcon
                  className="size-3.5 transition group-hover:translate-x-1"
                  aria-hidden="true"
                />
              </span>
            </Card>
          )
        })}
      </div>
    </section>
  )
}

// The launch, drawn: four hops across three parties where the order is the
// point. Only the exchange is in brand colour — the two hops that must be
// server-side.
const LANES = [
  { x: 168, label: 'Player', sub: 'their phone' },
  { x: 432, label: 'Gathering', sub: 'api.gathering.example' },
  { x: 700, label: 'Your server', sub: 'games.yourstudio.example' },
]
const STEPS = [
  { from: 0, to: 1, label: 'taps your tile' },
  { from: 1, to: 2, label: 'redirect to your entry', note: '?code=…  ·  one minute, one use' },
  { from: 2, to: 1, label: 'POST /oauth/token', note: 'client_id + client_secret', key: true },
  { from: 1, to: 2, label: 'access_token', note: 'expires_in 3600', key: true },
  { from: 2, to: 1, label: 'GET /v1/bridge/context' },
  { from: 1, to: 2, label: 'who arrived', note: 'player · room · scope · live' },
  { from: 2, to: 0, label: 'your app, already signed in' },
]
const TOP = 104
const GAP = 54
const BRAND = '#e5a000'
const INK = '#15172b'
const MUTED = '#5b5f7a'

export function LaunchFlow() {
  const y = (i) => TOP + i * GAP
  const height = y(STEPS.length - 1) + 44
  const bandTop = y(2) - 30
  const bandHeight = y(3) - y(2) + 46
  return (
    <figure className="not-prose my-10">
      <div className="skeu-well-soft overflow-hidden rounded-xl p-2">
        <svg
          viewBox={`0 0 792 ${height}`}
          role="img"
          aria-label="The launch flow. A player taps your tile; Gathering redirects them to your entry with a one-time code; your server exchanges that code for an access token; your server then asks the bridge who arrived and renders the app, already signed in."
          className="w-full"
          style={{ height: 'auto', color: MUTED }}
        >
          <defs>
            <marker
              id="lf-head"
              viewBox="0 0 10 10"
              refX="8"
              refY="5"
              markerWidth="5"
              markerHeight="5"
              orient="auto-start-reverse"
            >
              <path d="M0 0 L10 5 L0 10 z" fill="currentColor" />
            </marker>
            <marker
              id="lf-head-key"
              viewBox="0 0 10 10"
              refX="8"
              refY="5"
              markerWidth="5"
              markerHeight="5"
              orient="auto-start-reverse"
            >
              <path d="M0 0 L10 5 L0 10 z" fill={BRAND} />
            </marker>
          </defs>
          <rect
            x="52"
            y={bandTop}
            width="684"
            height={bandHeight}
            rx="12"
            fill={BRAND}
            opacity="0.07"
          />
          <rect
            x="52"
            y={bandTop}
            width="684"
            height={bandHeight}
            rx="12"
            fill="none"
            stroke={BRAND}
            strokeOpacity="0.3"
            strokeDasharray="4 4"
          />
          <text
            x="62"
            y={bandTop + 16}
            fontSize="10"
            fontWeight="700"
            letterSpacing="1.2"
            fill={BRAND}
          >
            SERVER TO SERVER
          </text>
          {LANES.map((lane) => (
            <g key={lane.label}>
              <rect
                x={lane.x - 74}
                y="22"
                width="148"
                height="44"
                rx="10"
                fill="#fff"
                stroke="currentColor"
                strokeOpacity="0.2"
              />
              <text
                x={lane.x}
                y="42"
                textAnchor="middle"
                fontSize="12.5"
                fontWeight="700"
                fill={INK}
              >
                {lane.label}
              </text>
              <text
                x={lane.x}
                y="57"
                textAnchor="middle"
                fontSize="9.5"
                fill="currentColor"
                opacity="0.8"
              >
                {lane.sub}
              </text>
              <line
                x1={lane.x}
                y1="72"
                x2={lane.x}
                y2={height - 12}
                stroke="currentColor"
                strokeWidth="1"
                strokeDasharray="2 5"
                opacity="0.4"
              />
            </g>
          ))}
          {STEPS.map((step, i) => {
            const x1 = LANES[step.from].x
            const x2 = LANES[step.to].x
            const forward = x2 > x1
            const start = forward ? x1 + 7 : x1 - 7
            const end = forward ? x2 - 7 : x2 + 7
            const mid = (start + end) / 2
            const stroke = step.key ? BRAND : 'currentColor'
            return (
              <g key={i}>
                <circle
                  cx="28"
                  cy={y(i)}
                  r="11"
                  fill="none"
                  stroke={stroke}
                  strokeOpacity={step.key ? 0.8 : 0.35}
                />
                <text
                  x="28"
                  y={y(i) + 4}
                  textAnchor="middle"
                  fontSize="11"
                  fontWeight="700"
                  fill={stroke}
                >
                  {i + 1}
                </text>
                <line
                  x1={start}
                  y1={y(i)}
                  x2={end}
                  y2={y(i)}
                  stroke={stroke}
                  strokeWidth={step.key ? 1.75 : 1.25}
                  markerEnd={`url(#${step.key ? 'lf-head-key' : 'lf-head'})`}
                />
                <text
                  x={mid}
                  y={y(i) - 9}
                  textAnchor="middle"
                  fontSize="12"
                  fontWeight={step.key ? 700 : 600}
                  fill={step.key ? BRAND : INK}
                >
                  {step.label}
                </text>
                {step.note && (
                  <text
                    x={mid}
                    y={y(i) + 15}
                    textAnchor="middle"
                    fontSize="10"
                    fill="currentColor"
                    opacity="0.8"
                  >
                    {step.note}
                  </text>
                )}
              </g>
            )
          })}
        </svg>
      </div>
      <figcaption className="mt-3 text-[13px] leading-relaxed text-ink-2">
        Steps 3 and 4 carry your client secret and the token it buys. Both happen server to server —
        neither is ever made from a browser, which is why there is no JavaScript SDK for this.
      </figcaption>
    </figure>
  )
}

export function NotFound() {
  return (
    <div className="not-prose flex flex-col items-start gap-4 py-10">
      <span className="skeu-tile-soft inline-flex size-14 items-center justify-center rounded-2xl">
        <CompassIcon className="size-6" aria-hidden="true" />
      </span>
      <h1 className="text-[28px] font-extrabold text-ink">There is no page here</h1>
      <p className="max-w-md text-[15px] leading-relaxed text-ink-2">
        The address does not match anything we publish. The pages are listed on the left, and the
        introduction is the place to start.
      </p>
      <Button href="/" arrow="right">
        Go to the introduction
      </Button>
    </div>
  )
}

// A guide card stays in this site when its page is here, and is a full page
// load into the other section when it is not.
function Card({ href, className, children }) {
  if (known(href)) {
    return (
      <Link to={href} className={className}>
        {children}
      </Link>
    )
  }
  return (
    <a href={href} className={className}>
      {children}
    </a>
  )
}
