import fs from 'node:fs'
import path from 'node:path'
import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkMdx from 'remark-mdx'
import remarkGfm from 'remark-gfm'
import remarkStringify from 'remark-stringify'

import { listPages } from './pages.mjs'
import { markdownFile, sectionOf } from '../src/section.js'

// The docs as text for AI models, written at build time beside the site:
//
//   llms.txt        the index (https://llmstxt.org): what Gathering is, the
//                   rules a model gets wrong first, and every page's copy
//   llms-full.txt   every page of the section in one file, for a model
//                   that reads one address
//   <page>.md       each page as Markdown: rooms.md for /rooms, index.md
//                   for the home page
//
// Made from the same MDX the site renders, so the two never disagree. The
// components become their Markdown: a <Property> a list item, a <Note> a
// quote, a <CodeGroup> its title over its fences, <Row> and <Col> nothing
// but their contents. A component with no Markdown form here fails the
// build, so a new one is never dropped from the copies without anybody
// knowing. Links become absolute and point at the copies, so a model can
// follow them. The app section gets its own three, under /app/, behind the
// same password as its pages.
//
// `npm run dev` serves them too, made on every request.

const ORIGIN = (process.env.DOCS_URL || 'https://docs.ga3da.fun').replace(/\/+$/, '')

export function llmsFiles() {
  return {
    name: 'llms-files',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const base = server.config.base
        const url = (req.url ?? '').split('?')[0]
        if (!url.startsWith(base)) return next()
        const files = buildFiles()
        const source = files[url.slice(base.length)]
        if (source === undefined) return next()
        res.setHeader('Content-Type', 'text/plain; charset=utf-8')
        res.end(source)
      })
    },
    generateBundle() {
      for (const [fileName, source] of Object.entries(buildFiles()))
        this.emitFile({ type: 'asset', fileName, source })
    },
  }
}

// Every file of the section, by its name under the section's base.
export function buildFiles() {
  const section = sectionOf(process.env.VITE_SECTION)
  const base = section === 'app' ? '/app/' : '/'
  const pages = listPages()
  const urlOf = (page) => `${ORIGIN}${base}${markdownFile(page, section)}`

  const files = {}
  const bodies = []
  for (const page of pages) {
    const body = pageMarkdown(page, { pages, urlOf })
    const text = `${body}\n---\n\nFrom the Gathering developer docs: ${ORIGIN}${page.href}. Every page: ${ORIGIN}${base}llms.txt\n`
    files[markdownFile(page, section)] = text
    bodies.push(body)
  }

  const intro = section === 'app' ? APP_INTRO : DEVELOPER_INTRO
  const groups = [...new Set(pages.map((p) => p.group))]
  const index = [
    intro.trim(),
    ...groups.map((group) =>
      [
        `## ${group}`,
        '',
        ...pages
          .filter((p) => p.group === group)
          .map((p) => `- [${p.title}](${urlOf(p)}): ${p.description}`),
      ].join('\n'),
    ),
    (section === 'app' ? APP_MORE : DEVELOPER_MORE).trim().replaceAll('{base}', `${ORIGIN}${base}`),
  ]
  files['llms.txt'] = index.join('\n\n') + '\n'

  const head = intro.trim().split('\n\n').slice(0, 2).join('\n\n')
  files['llms-full.txt'] =
    `${head}\n\nEvery page of ${ORIGIN}${base} in reading order, each from its \`# title\`. The index, with a link to each page alone: ${ORIGIN}${base}llms.txt\n\n` +
    bodies.join('\n\n') +
    '\n'
  return files
}

// The developer site's index: who it is for and the rules a model breaks
// first, then the pages (from pages.json), then the rest.
const DEVELOPER_INTRO = `
# Gathering developer docs

> Gathering (گعدة, "ga3da") is a phone app where friends open a private room and play a small game together, online or side by side. A game is an HTML page its studio hosts; the app loads it in a frame, seats the room's players, relays every message between them and keeps a shared state. These docs are for studios building one: the manifest, how a player arrives signed in, the \`window.gathering\` bridge, rooms and the relay, results, notifications, tournaments, and getting listed in the store.

Read these before writing code for Gathering:

- The game is a web page at the \`entry\` URL of its manifest, published at \`/.well-known/gathering-game.json\` on the studio's own origin. Gathering fetches it; nothing is uploaded.
- The page never opens a socket and never holds a session token. Everything it needs comes through \`window.gathering\`: the room, its members and messages, the shared state, the player, friends, and the app's chrome.
- A room plays rounds. \`room.end({ scores })\` finishes a round and the room goes back to its lobby with the same code and seats; the host starts the next one. Never make a new room to play again.
- A message sent to the room reaches every member, the sender included, in the same order with the same \`seq\` everywhere; tell your own echo by \`from\`. Applying moves on the echo, not on the send, keeps every phone in step.
- Lay the page out for a phone, Arabic first (right to left).
- A game's own server is optional. With one, it trades the launch's one-time code for a token at \`/oauth/token\` (the \`client_secret\` and the token never reach the page), verifies players with the room ticket against the JWKS, and may post a round's results and send players notifications.
- To build a whole game, give the model the minigame brief below: the rules, the manifest, the bootstrap, a template and a self-check in one file.
`

const DEVELOPER_MORE = `
## Build a game with an AI model

- [The minigame brief for AI models](${ORIGIN}/guide/MINIGAME_AI_BRIEF.md): one file to hand a model that builds a game: the rules, the manifest, the bootstrap, a working template, room patterns and a self-check.
- [The minigame guide](${ORIGIN}/guide/): the same, as a page for people, from the files to the checklist.

## Optional

- [Every page in one file]({base}llms-full.txt): the pages above, in reading order.
- [The console](https://console.ga3da.fun): where a studio registers its game, invites testers, sends a version to review and publishes it.
`

const APP_INTRO = `
# Gathering app API

> The API behind the Gathering (گعدة) phone app: sign-in and sessions, and the rest of what the app calls. For the mobile team; the public developer docs are ${ORIGIN}/llms.txt.

Every address under ${ORIGIN}/app/ is behind the same password as these pages.
`

const APP_MORE = `
## Optional

- [Every page in one file]({base}llms-full.txt): the pages above, in reading order.
`

// One page as Markdown, from its MDX.
const mdx = unified().use(remarkParse).use(remarkMdx).use(remarkGfm)
const md = unified().use(remarkGfm, { tablePipeAlign: false }).use(remarkStringify, {
  bullet: '-',
  emphasis: '_',
  fence: '`',
  fences: true,
  listItemIndent: 'one',
  rule: '-',
})

function pageMarkdown(page, ctx) {
  const file = `src/content/${page.file}.mdx`
  const tree = mdx.parse(fs.readFileSync(path.resolve(file), 'utf8'))
  tree.children = new Convert(page, ctx, file).flow(tree.children, tree)
  if (!tree.children.some((n) => n.type === 'heading' && n.depth === 1))
    tree.children.unshift(heading(1, [text(page.title)]))
  return md.stringify(tree).trim() + '\n'
}

class Convert {
  constructor(page, { pages, urlOf }, file) {
    this.page = page
    this.pages = pages
    this.urlOf = urlOf
    this.file = file
  }

  // A list of children, each converted, with every `{{ … }}` annotation
  // applied to the node before it and dropped.
  flow(children, parent) {
    const out = []
    for (const node of children) {
      if (node.type === 'mdxFlowExpression') {
        const props = this.expression(node)
        if (props) this.annotate(out.at(-1), props, out)
        continue
      }
      out.push(...this.node(node, parent))
    }
    return out
  }

  node(node, parent) {
    switch (node.type) {
      case 'mdxjsEsm':
        return []
      case 'mdxTextExpression': {
        const props = this.expression(node)
        if (props) this.annotate(parent, props, null)
        return []
      }
      case 'mdxJsxFlowElement':
      case 'mdxJsxTextElement':
        return this.component(node)
      case 'link':
      case 'definition':
        node.url = this.absolute(node.url)
        break
      case 'code': {
        // A fence's annotation rides in its info string: ```js {{ title: 'app.js' }}
        const title = node.meta?.match(/\{\{.*\btitle:\s*(['"])(.*?)\1.*\}\}/)
        if (title) node.meta = `title="${title[2]}"`
        else if (node.meta?.includes('{{')) throw new Error(`llms: a fence's annotation (${this.where(node)})`)
        break
      }
    }
    if (node.children) {
      node.children = this.flow(node.children, node)
      trimEnd(node)
    }
    const after = node.data?.after ?? []
    return [node, ...after]
  }

  // `{{ title: 'cURL' }}` and the like: the object, or null for a comment.
  expression(node) {
    const program = node.data?.estree
    if (!program || program.body.length === 0) return null
    return literal(program.body[0].expression, this.where(node))
  }

  // What an annotation means in Markdown: a fence's title in its info
  // string, a heading's route on a line under it. A paragraph's class is
  // only looks.
  annotate(target, props, out) {
    if (!target) return
    if (target.type === 'code' && props.title) target.meta = `title="${props.title}"`
    if (target.type === 'heading' && (props.tag || props.label)) {
      const route = paragraph([inlineCode([props.tag, props.label].filter(Boolean).join(' '))])
      if (out) out.push(route)
      else (target.data ??= {}).after = [route]
    }
  }

  component(node) {
    const attrs = attributes(node, this.where(node))
    const kids = () => blocks(this.flow(node.children, node))
    switch (node.name) {
      case 'Row':
      case 'Col':
        return this.flow(node.children, node)
      case 'Note': {
        const body = kids()
        if (body[0]?.type === 'paragraph') body[0].children.unshift(strong('Note:'), text(' '))
        else body.unshift(paragraph([strong('Note:')]))
        return [{ type: 'blockquote', children: body }]
      }
      case 'CodeGroup': {
        const head = attrs.title ? [strong(attrs.title)] : []
        const route = [attrs.tag, attrs.label].filter(Boolean).join(' ')
        if (route) head.push(text(head.length ? ' ' : ''), inlineCode(route))
        return [...(head.length ? [paragraph(head)] : []), ...this.flow(node.children, node)]
      }
      case 'Properties':
        return [list(node.children.flatMap((child) => this.node(child, node)).map(asItem))]
      case 'Property': {
        const lead = [inlineCode(attrs.name)]
        if (attrs.type) lead.push(text(' ('), inlineCode(attrs.type), text(attrs.required ? ', required)' : ')'))
        else if (attrs.required) lead.push(text(' (required)'))
        const body = kids()
        if (body[0]?.type === 'paragraph') body[0].children.unshift(...lead, text(' — '))
        else body.unshift(paragraph(lead))
        return [{ type: 'listItem', spread: body.length > 1, children: body }]
      }
      case 'Guides':
        return [
          ...(attrs.title ? [heading(2, [text(attrs.title)])] : []),
          list(
            (attrs.guides ?? []).map((g) =>
              listItem([paragraph([link(this.absolute(g.href), g.name), text(`: ${g.description}`)])]),
            ),
          ),
        ]
      case 'Hero':
      case 'Player':
      case 'LaunchFlow':
        return this.flow(mdx.parse(STAND_INS[node.name]).children, node)
      default:
        throw new Error(
          `llms: <${node.name}> (${this.where(node)}) has no Markdown form; give it one in mdx/llms.mjs`,
        )
    }
  }

  // A link inside the docs as an address a model can fetch: a page's
  // Markdown copy, anything else on the docs host in full.
  absolute(href) {
    if (href.startsWith('#')) return this.urlOf(this.page) + href
    if (!href.startsWith('/')) return href
    const [pathname, hash] = href.split('#')
    const target = this.pages.find((p) => p.href === (pathname.replace(/\/+$/, '') || '/'))
    return (target ? this.urlOf(target) : ORIGIN + pathname) + (hash ? `#${hash}` : '')
  }

  where(node) {
    return `${this.file}:${node.position?.start.line ?? '?'}`
  }
}

// What the drawn components say, for a reader who cannot see them. Keep each
// in step with its component in src/components/blocks.jsx.
const STAND_INS = {
  Hero: `
**Build a game people play together in one room.** Gathering is where friends open a room and play. Your game appears in it as a tile: they tap it, the room is already made, the players already in it, every message already relayed. No accounts, no sockets, no lobby to write. Start with [the manifest](/manifest), then [the bridge](/bridge).

The whole of a manifest, at \`/.well-known/gathering-game.json\` on your origin:

\`\`\`json
{
  "id": "yourstudio.game",
  "name": "اسم لعبتك",
  "publisher": { "tier": "partner", "name": "اسم استوديوك" },
  "version": "1.0.0",
  "mode": "embedded",
  "entry": "https://games.yourstudio.example/",
  "orientation": "portrait",
  "room": {
    "players": { "min": 2, "max": 8 },
    "host": "peer",
    "pace": "realtime"
  }
}
\`\`\`
`,
  Player: `
## What a player sees

- **Your tile**: in the store, with your name, your icon and one line about what it does.
- **Your game**: open, in a room, with the players already in it. Nothing to type.
- **Their inbox**: a line from you lands here, with your name on it, whenever you send one.
`,
  LaunchFlow: `
**The launch flow**, between the player's phone, Gathering and your server:

1. The player taps your tile.
2. Gathering redirects the player to your \`entry\` with \`?code=…\`: one minute, one use.
3. Your server calls \`POST /oauth/token\` with your \`client_id\` and \`client_secret\` (server side only).
4. Gathering answers an \`access_token\`, \`expires_in\` 3600.
5. Your server calls \`GET /v1/bridge/context\`.
6. Gathering answers who arrived: the player, the room, the scope, and whether it is live.
7. Your server renders your app to the player, already signed in.
`,
}

// A JSX element's attributes as values: strings, true for a bare name, and
// the literal of an expression (`guides={[…]}`).
function attributes(node, where) {
  const out = {}
  for (const a of node.attributes) {
    if (a.type !== 'mdxJsxAttribute') throw new Error(`llms: a spread attribute (${where})`)
    if (a.value === null || a.value === undefined) out[a.name] = true
    else if (typeof a.value === 'string') out[a.name] = a.value
    else out[a.name] = literal(a.value.data.estree.body[0].expression, where)
  }
  return out
}

// The value of a literal written in a page: objects, arrays, strings,
// numbers, booleans and template strings without holes. Anything else is
// code, which a page has no business carrying into a copy.
function literal(n, where) {
  switch (n.type) {
    case 'ObjectExpression':
      return Object.fromEntries(
        n.properties.map((p) => [p.key.name ?? p.key.value, literal(p.value, where)]),
      )
    case 'ArrayExpression':
      return n.elements.map((e) => literal(e, where))
    case 'Literal':
      return n.value
    case 'TemplateLiteral':
      if (n.expressions.length === 0) return n.quasis.map((q) => q.value.cooked).join('')
      break
  }
  throw new Error(`llms: ${n.type} is not a literal (${where})`)
}

const PHRASING = new Set([
  'text', 'inlineCode', 'emphasis', 'strong', 'delete', 'link', 'linkReference', 'break', 'image', 'html',
])

// Children that are phrasing alone (a component written on one line) in a
// paragraph, so they can stand in a quote or a list item.
function blocks(nodes) {
  return nodes.length > 0 && nodes.every((n) => PHRASING.has(n.type)) ? [paragraph(nodes)] : nodes
}

// The space before an annotation, left at the end of a heading or a paragraph.
function trimEnd(node) {
  const last = node.children.at(-1)
  if ((node.type === 'heading' || node.type === 'paragraph') && last?.type === 'text')
    last.value = last.value.replace(/\s+$/, '')
}

const asItem = (n) => (n.type === 'listItem' ? n : listItem(blocks([n])))
const text = (value) => ({ type: 'text', value })
const inlineCode = (value) => ({ type: 'inlineCode', value })
const strong = (value) => ({ type: 'strong', children: [text(value)] })
const link = (url, value) => ({ type: 'link', url, children: [text(value)] })
const paragraph = (children) => ({ type: 'paragraph', children })
const heading = (depth, children) => ({ type: 'heading', depth, children })
const listItem = (children) => ({ type: 'listItem', spread: false, children })
const list = (children) => ({
  type: 'list',
  ordered: false,
  spread: children.some((c) => c.spread),
  children,
})
