# Developer docs

The pages a game developer reads to build a game for Gathering. Written in
MDX under `src/content/`, one file per page, listed in `pages.json` with
their titles and groups. A React shell around them, built with Vite to
static files.

    npm install
    npm run dev      # http://localhost:5173, the developer site
    npm run dev:app  # http://localhost:5173/app/, the app section
    npm run build    # dist/ and dist-app/, which the Dockerfile hands to nginx

Two sites come out of one tree. Pages in the "The app" group are the mobile
team's, built on their own under `/app/`, and nginx keeps them behind
`DOCS_USER` and `DOCS_PASSWORD`. Everything else is the public developer site.

`guide/` is the minigame guide, served at `/guide/` beside the site: one page
and the brief for AI models, built in `../../../gathering_docs` (`python3
build.py` writes them here). Edit them there, never here.

Each build also writes the docs as text for AI models (`mdx/llms.mjs`):
`llms.txt` (the index, https://llmstxt.org), `llms-full.txt` (every page in
one file) and a `.md` beside every page (`/rooms.md` for `/rooms`,
`/index.md` for the home page), made from the same MDX. The app section gets
its own under `/app/`, behind its password. A component with no Markdown form
in `mdx/llms.mjs` fails the build; a page's "Copy page" key copies its `.md`.
`npm run dev` serves them too.

Fences are coloured at build time. `{{ title: 'cURL' }}` after a fence names
its tab; `{{ tag: 'GET', label: '/v1/…' }}` after a heading draws the route
above it; `{{ className: 'lead' }}` after a paragraph sets it large.
