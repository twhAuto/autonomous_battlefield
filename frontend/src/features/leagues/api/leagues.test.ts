import { beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelLeague, createLeague, getLeague, LeagueApiError, listLeagues, type CreateLeagueInput } from './leagues'
import { controllerName } from '../../../shared/domain/controllers'
import { getAnalytics } from '../../analytics/api/analytics'

const input = { id: 'league', name: 'Cup', scenarioIds: ['frontier'], tickCount: 500, entrants: [] } as CreateLeagueInput
const reply = (status: number, body: unknown = {}) => new Response(JSON.stringify(body), { status })

describe('league requests', () => {
  beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })

  it('reads, creates and cancels leagues', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(reply(200, { leagues: [{ id: 'a' }] })).mockResolvedValueOnce(reply(200, { id: 'a' }))
      .mockResolvedValueOnce(reply(201, { id: 'a' })).mockResolvedValueOnce(reply(200, { id: 'a', status: 'cancelled' }))
    expect(await listLeagues()).toEqual([{ id: 'a' }])
    expect(await getLeague('a')).toEqual({ id: 'a' })
    expect(await createLeague(input)).toEqual({ id: 'a' })
    expect(vi.mocked(fetch).mock.calls[2][1]).toMatchObject({ method: 'POST', body: JSON.stringify(input) })
    expect((await cancelLeague('a')).status).toBe('cancelled')
    expect(String(vi.mocked(fetch).mock.calls[3][0])).toContain('/v1/leagues/a/cancel')
  })

  it('explains failures', async () => {
    for (const [status, text] of [[400, 'four different factions'], [403, 'Only administrators'], [404, 'no longer exists'], [409, 'already in use'], [422, 'model is unavailable'], [500, 'could not be started']] as const) {
      vi.mocked(fetch).mockResolvedValueOnce(reply(status))
      await expect(createLeague(input)).rejects.toThrow(text)
    }
    vi.mocked(fetch).mockResolvedValueOnce(reply(404)).mockResolvedValueOnce(reply(500)).mockResolvedValueOnce(reply(500))
      .mockResolvedValueOnce(reply(409)).mockResolvedValueOnce(reply(500))
    await expect(getLeague('a')).rejects.toThrow('could not be found')
    await expect(getLeague('a')).rejects.toThrow('could not be loaded')
    await expect(listLeagues()).rejects.toBeInstanceOf(LeagueApiError)
    await expect(cancelLeague('a')).rejects.toThrow('already finished')
    await expect(cancelLeague('a')).rejects.toThrow('could not be cancelled')
  })
})

describe('analytics requests', () => {
  beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })

  it('sends only the chosen filters', async () => {
    vi.mocked(fetch).mockImplementation(async () => reply(200, { battles: 0 }))
    await getAnalytics({})
    await getAnalytics({ leagueId: 'x', allRules: true, excludeFallback: true })
    expect(String(vi.mocked(fetch).mock.calls[0][0])).toMatch(/\/v1\/analytics$/)
    expect(String(vi.mocked(fetch).mock.calls[1][0])).toContain('/v1/analytics?leagueId=x&rulesVersion=all&excludeFallback=true')
  })

  it('explains failures and names controllers', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(reply(403)).mockResolvedValueOnce(reply(500))
    await expect(getAnalytics({})).rejects.toThrow('developers and administrators')
    await expect(getAnalytics({})).rejects.toThrow('could not be loaded')
    expect(controllerName({ type: 'scripted', profileId: 'ignored' })).toBe('Scripted')
    expect(controllerName({ type: 'model', profileId: 'claude' })).toBe('claude')
    expect(controllerName({ type: 'model', modelIdentifier: 'sonnet', reasoningEffort: 'low' })).toBe('sonnet · low')
  })
})
