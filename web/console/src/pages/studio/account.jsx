import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'

import { api } from '../../api.js'
import { Fact, Note } from '../../components/game-bits.jsx'
import { useMe } from '../../lib/me.js'

// The publisher's own account: who it is, and a new password. Forgetting the
// password is a new link from the گعدة team.
export default function StudioAccount() {
  const me = useMe()
  const [current, setCurrent] = useState('')
  const [password, setPassword] = useState('')
  const [again, setAgain] = useState('')
  const change = useMutation({
    mutationFn: () => api.post('/v1/publisher/password', { current, password }),
    onSuccess: () => {
      setCurrent('')
      setPassword('')
      setAgain('')
    },
  })
  const short = password.length > 0 && password.length < 10
  const differs = again.length > 0 && again !== password

  return (
    <div className="grid max-w-xl gap-5">
      <div>
        <h1 className="text-2xl font-extrabold">Account</h1>
        <p className="text-ink-2 mt-1 text-sm">Your studio’s sign-in to the گعدة console.</p>
      </div>
      <section className="card grid gap-3 p-4">
        <Fact label="Studio">{me?.name}</Fact>
        <Fact label="Email" mono>
          {me?.email}
        </Fact>
        <p className="text-ink-3 text-xs">The name shows on your games in the store. To change either, ask the گعدة team.</p>
      </section>
      <section className="card p-4">
        <h2 className="text-sm font-extrabold">A new password</h2>
        <form
          className="mt-3 grid gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (!short && !differs) change.mutate()
          }}
        >
          <label className="grid gap-1 text-sm font-semibold">
            The password now
            <input
              className="field font-normal"
              type="password"
              autoComplete="current-password"
              required
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </label>
          <label className="grid gap-1 text-sm font-semibold">
            The new one
            <input
              className="field font-normal"
              type="password"
              autoComplete="new-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <span className={`text-xs font-normal ${short ? 'text-warn' : 'text-ink-3'}`}>At least 10 characters.</span>
          </label>
          <label className="grid gap-1 text-sm font-semibold">
            The new one again
            <input
              className="field font-normal"
              type="password"
              autoComplete="new-password"
              required
              value={again}
              onChange={(e) => setAgain(e.target.value)}
            />
            {differs && <span className="text-warn text-xs font-normal">The two do not match.</span>}
          </label>
          {change.isError && <p className="text-bad text-sm">{change.error.message}</p>}
          {change.isSuccess && <Note tone="good">Changed. Your other sessions stay signed in.</Note>}
          <div className="flex justify-end">
            <button
              type="submit"
              className="btn btn-primary h-9 px-4 text-xs"
              disabled={change.isPending || short || differs || !current || !password || !again}
            >
              {change.isPending ? 'Saving…' : 'Change it'}
            </button>
          </div>
        </form>
      </section>
    </div>
  )
}
