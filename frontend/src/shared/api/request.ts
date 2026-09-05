export const authenticationRequiredEvent = 'battlefield:authentication-required'

export async function apiFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const response = await fetch(input, init)
  if (response.status === 401 && typeof window !== 'undefined') {
    window.dispatchEvent(new Event(authenticationRequiredEvent))
  }
  return response
}
