import { type SubmitEvent, useState } from 'react'
import type { AuthenticationClient, AuthSession } from '../api/auth'

interface LoginPageProps {
  readonly authentication: AuthenticationClient
  readonly onAuthenticated: (session: AuthSession) => void
}

export function LoginPage({ authentication, onAuthenticated }: Readonly<LoginPageProps>) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      onAuthenticated(await authentication.login(username.trim(), password))
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : 'Login could not be completed.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="login-page">
      <section className="login-card" aria-labelledby="login-title">
        <div className="login-card__mark" aria-hidden="true">A</div>
        <span className="login-card__eyebrow">RESTRICTED COMMAND INTERFACE</span>
        <h1 id="login-title">Identify commander.</h1>
        <p>Authenticate before entering the autonomous battlefield control plane.</p>
        <form onSubmit={submit}>
          <label htmlFor="login-username">Username</label>
          <input
            id="login-username"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            required
          />
          <label htmlFor="login-password">Password</label>
          <input
            id="login-password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            required
          />
          {error && <p className="login-card__error" role="alert">{error}</p>}
          <button className="submit-button" type="submit" disabled={submitting}>
            <span>{submitting ? 'AUTHENTICATING…' : 'ENTER COMMAND POST'}</span>
            <span>→</span>
          </button>
        </form>
      </section>
    </main>
  )
}
