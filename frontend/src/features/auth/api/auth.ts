// Authentication client for Access or development sessions, login, and logout; keeps
// expected login failures local to the form.

import { createApiClient } from '../../../shared/api/request'

export interface AuthSession {
  enabled: boolean
  authenticated: boolean
  mode?: string
  capabilities?: { inspect: boolean; cli: boolean; admin: boolean }
  actor?: { id: string; email?: string; role?: 'player' | 'developer' | 'admin' }
}

export interface AuthenticationClient {
  session(): Promise<AuthSession>
  login(username: string, password: string): Promise<AuthSession>
  logout(): Promise<void>
}

export class AuthenticationError extends Error {
  constructor(message: string, public readonly status?: number) {
    super(message)
    this.name = 'AuthenticationError'
  }
}

const authFetch = createApiClient(AuthenticationError, false)

async function session(): Promise<AuthSession> {
  const response = await authFetch(`/v1/auth/session`, { credentials: 'same-origin' })
  if (response.status === 401) {
    try { return await response.json() as AuthSession } catch { return { enabled: true, authenticated: false, mode: 'cloudflare' } }
  }
  if (!response.ok) {
    throw new AuthenticationError('Authentication status could not be checked.', response.status)
  }
  return (await response.json()) as AuthSession
}

/** Establishes the session cookie, then reads the session endpoint to obtain the authenticated actor. */
async function login(username: string, password: string): Promise<AuthSession> {
  const response = await authFetch(`/v1/auth/login`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    })
  if (response.status === 401) {
    throw new AuthenticationError('The username or password is incorrect.', response.status)
  }
  if (!response.ok) {
    throw new AuthenticationError('Login could not be completed.', response.status)
  }
  return session()
}

async function logout(): Promise<void> {
  const response = await authFetch('/v1/auth/logout', {
    method: 'POST',
    credentials: 'same-origin',
  })
  if (!response.ok) throw new AuthenticationError('Logout could not be completed.', response.status)
  if (response.headers.get('Content-Type')?.includes('application/json')) { const body = await response.json() as { logoutUrl?: string }; if (body.logoutUrl === '/cdn-cgi/access/logout') window.location.assign(body.logoutUrl) }
}

export const authenticationClient: AuthenticationClient = { session, login, logout }
