import { beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelLeague, createLeague, disqualifyEntrant, getLeague, LeagueApiError, listLeagues, type CreateLeagueInput } from './leagues'
import { controllerName } from '../../../shared/domain/controllers'
import { getAnalytics } from '../../analytics/api/analytics'

const input = { id: 'league', name: 'Cup', scenarioIds: ['frontier'], tickCount: 500, entrants: [] } as CreateLeagueInput
const reply = (status: number, body: unknown = {}) => new Response(JSON.stringify(body), { status })

describe('league requests', () => {
  beforeEach(() => { vi.stubGlobal('fetch', vi.fn()) })

  it('reads, creates and cancels leagues', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(reply(200, { leagues: [{ id: 'a' }], scheduling: { battleSlots: 4, cliSlots: 4 } })).mockResolvedValueOnce(reply(200, { id: 'a' }))
      .mockResolvedValueOnce(reply(201, { id: 'a' })).mockResolvedValueOnce(reply(200, { id: 'a', status: 'cancelled' }))
    expect(await listLeagues()).toEqual({ leagues: [{ id: 'a' }], scheduling: { battleSlots: 4, cliSlots: 4 } })
    expect(await getLeague('a')).toEqual({ id: 'a' })
    expect(await createLeague(input)).toEqual({ id: 'a' })
    expect(vi.mocked(fetch).mock.calls[2][1]).toMatchObject({ method: 'POST', body: JSON.stringify(input) })
    expect((await cancelLeague('a')).status).toBe('cancelled')
    expect(String(vi.mocked(fetch).mock.calls[3][0])).toContain('/v1/leagues/a/cancel')
    vi.mocked(fetch).mockResolvedValueOnce(reply(200, { leagues: [] }))
    await listLeagues(undefined, { rulesVersion: '13' })
    expect(String(vi.mocked(fetch).mock.calls[4][0])).toBe('/v1/leagues?rulesVersion=13')
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

  it('disqualifies an entrant with its reason and explains refusals', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(reply(200, { id: 'a' }))
    expect(await disqualifyEntrant('a', 6, 'Too slow')).toEqual({ id: 'a' })
    expect(String(vi.mocked(fetch).mock.calls[0][0])).toContain('/v1/leagues/a/entrants/6/disqualify')
    expect(vi.mocked(fetch).mock.calls[0][1]).toMatchObject({ method: 'POST', body: JSON.stringify({ reason: 'Too slow' }) })
    for (const [status, body, text] of [
      [400, {}, '200 characters'], [403, {}, 'Only administrators'], [404, {}, 'no longer exists'],
      [409, { error: { message: 'entrant has no match left to play' } }, 'cannot be disqualified: entrant has no match left to play.'],
      [409, {}, 'could not be disqualified'], [500, {}, 'could not be disqualified'],
    ] as const) {
      vi.mocked(fetch).mockResolvedValueOnce(reply(status, body))
      await expect(disqualifyEntrant('a', 6, '')).rejects.toThrow(text)
    }
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
    await expect(getAnalytics({})).rejects.toThrow('cannot view statistics')
    await expect(getAnalytics({})).rejects.toThrow('could not be loaded')
    expect(controllerName({ type: 'scripted', profileId: 'ignored' })).toBe('Scripted')
    expect(controllerName({ type: 'model', profileId: 'claude' })).toBe('claude')
    expect(controllerName({ type: 'model', modelIdentifier: 'sonnet', reasoningEffort: 'low' })).toBe('sonnet · low')
  })
})
