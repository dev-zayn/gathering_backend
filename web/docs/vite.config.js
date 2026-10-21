import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import mdx from '@mdx-js/rollup'
import path from 'node:path'

import { remarkPlugins } from './mdx/remark.mjs'
import { rehypePlugins } from './mdx/rehype.mjs'
import { recmaPlugins } from './mdx/recma.mjs'
import { searchIndex } from './mdx/search.mjs'
import { pagesList } from './mdx/pages.mjs'

const app = process.env.VITE_SECTION === 'app'

// The docs are pages written in MDX and a React shell around them, built to
// static files that nginx serves. Nothing runs at request time: the one
// address a developer is given before they have an account must not depend on
// a process that can be down.
export default defineConfig({
  plugins: [
    {
      enforce: 'pre',
      ...mdx({ providerImportSource: '@mdx-js/react', remarkPlugins, rehypePlugins, recmaPlugins }),
    },
    react({ include: /\.(jsx|js|mdx|md)$/ }),
    tailwindcss(),
    searchIndex(),
    pagesList(),
  ],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, 'src') },
  },
  // The app section is its own build, served under /app/ behind a password.
  // VITE_SECTION=app picks it; nothing else differs.
  base: app ? '/app/' : '/',
  build: { outDir: app ? 'dist-app' : 'dist', emptyOutDir: true },
})
