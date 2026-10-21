import React from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'

import App from './app.jsx'
import { queries } from './lib/query.js'
import './index.css'

createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <QueryClientProvider client={queries}>
      <App />
    </QueryClientProvider>
  </React.StrictMode>,
)
