// Shared fetch transport for the configured API origin, authentication-expiry
// notifications, and consistent feature-level network errors.

const API_URL = (import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '')
const unreachableMessage = 'The command post is unreachable. Check that the battlefield server is running.'

export const authenticationRequiredEvent = 'battlefield:authentication-required'

class PreviewReadOnlyError extends Error {}
let previewRole: 'developer' | 'player' | null = null
export function setRequestPreviewRole(role: typeof previewRole) { previewRole = role }

/** Sends an API request and optionally notifies the app of a 401; callers handle the response status. */
export async function apiFetch(path: string, init?: RequestInit, notifyUnauthorized = true): Promise<Response> {
  if (previewRole && init?.method && !['GET', 'HEAD'].includes(init.method.toUpperCase())) throw new PreviewReadOnlyError('Role preview is read-only. Exit preview to make changes.')
  const response = await fetch(`${API_URL}${path}`, { ...init, headers: { ...(previewRole ? { 'X-Battlefield-Preview-Role': previewRole } : {}), ...Object.fromEntries(new Headers(init?.headers)), 'X-Requested-With': 'XMLHttpRequest' } })
  if (notifyUnauthorized && response.status === 401 && typeof window !== 'undefined') {
    window.dispatchEvent(new Event(authenticationRequiredEvent))
  }
  return response
}

// Feature clients retain their error types; cancellation remains distinguishable.
export function createApiClient(ErrorType: new (message: string) => Error, notifyUnauthorized = true) {
  return async (path: string, init?: RequestInit): Promise<Response> => {
    try {
      return await apiFetch(path, init, notifyUnauthorized)
    } catch (error) {
      if (error instanceof PreviewReadOnlyError) throw new ErrorType(error.message)
      if (error instanceof DOMException && error.name === 'AbortError') throw error
      throw new ErrorType(unreachableMessage)
    }
  }
}
