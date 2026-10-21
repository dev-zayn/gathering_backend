import { QueryClient } from '@tanstack/react-query'

// One cache of what the API said, shared by every screen. A read stays fresh
// for half a minute; a refusal (4xx) is the answer and is not retried, only a
// failure to get one (the network, a timeout, a 5xx), twice.
export const queries = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      networkMode: 'always',
      retry: (failures, error) => failures < 2 && !(error?.status >= 400 && error.status < 500),
    },
    mutations: { networkMode: 'always' },
  },
})
