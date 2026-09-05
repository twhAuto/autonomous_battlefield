const API_URL = (import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '')

export interface AuthSession {
  enabled: boolean
  authenticated: boolean
  actor?: { id: string }
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

async function session(): Promise<AuthSession> {
  let response: Response
  try {
    response = await fetch(`${API_URL}/v1/auth/session`, { credentials: 'same-origin' })
  } catch {
    throw new AuthenticationError('The command post is unreachable. Check that the battlefield server is running.')
  }
  if (response.status === 401) {
    return { enabled: true, authenticated: false }
  }
  if (!response.ok) {
    throw new AuthenticationError('Authentication status could not be checked.', response.status)
  }
  return (await response.json()) as AuthSession
}

async function login(username: string, password: string): Promise<AuthSession> {
  let response: Response
  try {
    response = await fetch(`${API_URL}/v1/auth/login`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    })
  } catch {
    throw new AuthenticationError('The command post is unreachable. Check that the battlefield server is running.')
  }
  if (response.status === 401) {
    throw new AuthenticationError('The username or password is incorrect.', response.status)
  }
  if (!response.ok) {
    throw new AuthenticationError('Login could not be completed.', response.status)
  }
  return session()
}

async function logout(): Promise<void> {
  try {
    const response = await fetch(`${API_URL}/v1/auth/logout`, {
      method: 'POST',
      credentials: 'same-origin',
    })
    if (!response.ok) throw new AuthenticationError('Logout could not be completed.', response.status)
  } catch (error) {
    if (error instanceof AuthenticationError) throw error
    throw new AuthenticationError('The command post is unreachable. Check that the battlefield server is running.')
  }
}

export const authenticationClient: AuthenticationClient = { session, login, logout }
