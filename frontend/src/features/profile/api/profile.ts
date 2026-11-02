// Display-name rules and the request that saves the caller's own name; the server
// applies the same rules and also requires the name to be unique ignoring case.

import { createApiClient, errorMessage } from '../../../shared/api/request'
import type { AuthSession } from '../../auth/api/auth'

export const displayNameHint = 'Use 3 to 24 letters, digits, spaces, hyphens or underscores.'
const displayNamePattern = /^[A-Za-z0-9_-][A-Za-z0-9 _-]{1,22}[A-Za-z0-9_-]$/

/** Returns the trimmed name, or null when it breaks the display-name rules. */
export function normalizeDisplayName(name: string): string | null {
  const trimmed = name.trim()
  return displayNamePattern.test(trimmed) ? trimmed : null
}

/** Players and Testers must choose a name before using the app; Admins and Guests never do. */
export function needsDisplayName(session: AuthSession): boolean {
  const role = session.actor?.role
  return (role === 'player' || role === 'tester') && !session.actor?.displayName
}

/** The name the top bar shows for the signed-in person, never their email or ID. */
export function signedInName(session: AuthSession): string {
  if (session.actor?.displayName) return session.actor.displayName
  const role = session.actor?.role ?? (session.mode === 'cloudflare' ? 'player' : 'admin')
  if (role === 'admin') return 'Admin'
  if (role === 'guest') return 'Guest'
  return 'Unnamed commander'
}

export class ProfileApiError extends Error {
  constructor(message: string, public readonly status?: number) {
    super(message)
    this.name = 'ProfileApiError'
  }
}

const profileFetch = createApiClient(ProfileApiError)

/** Saves the caller's display name and returns the stored, trimmed name. */
export async function setDisplayName(displayName: string): Promise<string> {
  const response = await profileFetch('/v1/profile', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ displayName }),
  })
  if (response.status === 409) throw new ProfileApiError('That name is taken. Choose another.', 409)
  if (response.status === 400) throw new ProfileApiError(displayNameHint, 400)
  if (!response.ok) throw new ProfileApiError(await errorMessage(response) || 'Your display name could not be saved. Try again.', response.status)
  return ((await response.json()) as { displayName: string }).displayName
}
