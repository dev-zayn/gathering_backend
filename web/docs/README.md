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

Fences are coloured at build time. `{{ title: 'cURL' }}` after a fence names
its tab; `{{ tag: 'GET', label: '/v1/…' }}` after a heading draws the route
above it; `{{ className: 'lead' }}` after a paragraph sets it large.
