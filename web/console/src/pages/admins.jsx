import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '../api.js'
import { useMe } from '../lib/me.js'

// Everybody who has signed in to the console, and the one lever: sign one of
// them out everywhere.
export default function Admins() {
  const me = useMe()
  const queries = useQueryClient()
  const list = useQuery({ queryKey: ['admins'], queryFn: () => api.get('/v1/admin/admins') })
  const end = useMutation({
    mutationFn: (id) => api.del(`/v1/admin/admins/${id}/sessions`),
    onSuccess: () => queries.invalidateQueries({ queryKey: ['admins'] }),
  })
  const canEnd = me?.roles?.includes('admin')

  return (
    <div>
      <h1 className="text-xl font-extrabold">Admins</h1>
      <p className="text-ink-2 mt-1 text-sm">Roles live at the identity provider; this lists who has been here.</p>
      <div className="card mt-4 divide-y divide-line">
        {list.isLoading && <div className="text-ink-3 p-4 text-sm">Loading…</div>}
        {list.isError && <div className="text-bad p-4 text-sm">{list.error.message}</div>}
        {list.data?.admins?.map((a) => (
          <div key={a.id} className="flex items-center gap-3 p-4">
            <div className="min-w-0 flex-1">
              <div className="font-semibold">
                {a.name || a.id} {a.id === me?.id && <span className="text-ink-3 text-xs">(you)</span>}
              </div>
              <div className="text-ink-2 text-xs">
                {a.email || '—'} · last sign-in {a.seen_at ? a.seen_at.slice(0, 16).replace('T', ' ') : 'never'}
              </div>
            </div>
            {canEnd && a.id !== me?.id && (
              <button
                type="button"
                className="btn btn-secondary h-8 px-3 text-xs"
                disabled={end.isPending}
                onClick={() => end.mutate(a.id)}
              >
                Sign out everywhere
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
