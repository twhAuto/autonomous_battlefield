// Plays a finished battle from replay segments: loads the segment containing the current
// tick, prefetches the next one, advances on a timer and keeps a bounded segment cache.

import { useCallback, useEffect, useRef, useState } from 'react'
import { BattleApiError, getBattleReplay, type BattleReplay, type ReplayFrame } from '../api/battles'

export const replaySpeeds = [1, 2, 4, 8] as const
export type ReplaySpeed = typeof replaySpeeds[number]
/** Playback interval at 1×; faster speeds divide it. */
export const replayTickMilliseconds = 600
/** A seek waits this long before fetching, so dragging the timeline loads only where it stops. */
export const replaySeekDelayMilliseconds = 150
/** Segments kept in memory; the ones farthest from the current tick are dropped first. */
export const replayCachedSegments = 6

export interface BattleReplayControls {
  tick: number
  frame: ReplayFrame | null
  playing: boolean
  buffering: boolean
  speed: ReplaySpeed
  error: string
  play: () => void
  pause: () => void
  seek: (tick: number) => void
  step: (offset: number) => void
  setSpeed: (speed: ReplaySpeed) => void
  retry: () => void
}

function covering(segments: BattleReplay[], tick: number) {
  return segments.find((segment) => segment.fromTick <= tick && tick <= segment.toTick)
}

/** Replays battle `id` from tick 0 to `finalTick`; mount it only for a settled battle. */
export function useBattleReplay(id: string, finalTick: number): BattleReplayControls {
  const [segments, setSegments] = useState<BattleReplay[]>([])
  const [tick, setTick] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState<ReplaySpeed>(1)
  const [error, setError] = useState('')
  const inFlight = useRef(new Set<number>())
  const latestTick = useRef(0)
  const controller = useRef<AbortController | null>(null)

  useEffect(() => { latestTick.current = tick }, [tick])
  useEffect(() => {
    const current = new AbortController()
    controller.current = current
    return () => current.abort()
  }, [id])

  const load = useCallback(async (target: number) => {
    const signal = controller.current?.signal
    if (!signal || inFlight.current.has(target)) return
    inFlight.current.add(target)
    try {
      const segment = await getBattleReplay(id, target, signal)
      setSegments((cached) => {
        const distance = (entry: BattleReplay) => Math.abs(entry.fromTick - latestTick.current)
        return [...cached.filter((entry) => entry.fromTick !== segment.fromTick), segment]
          .sort((a, b) => distance(a) - distance(b))
          .slice(0, replayCachedSegments)
      })
    } catch (loadError) {
      if (signal.aborted) return
      setPlaying(false)
      setError(loadError instanceof BattleApiError ? loadError.message : 'The battle replay could not be loaded.')
    } finally {
      inFlight.current.delete(target)
    }
  }, [id])

  const current = covering(segments, tick)
  const frame = current?.frames[tick - current.fromTick] ?? null
  const nextTick = current?.nextTick ?? null
  const nextLoaded = nextTick === null || covering(segments, nextTick) !== undefined
  const following = covering(segments, tick + 1) !== undefined

  useEffect(() => {
    if (current || error) return
    const timer = setTimeout(() => void load(tick), replaySeekDelayMilliseconds)
    return () => clearTimeout(timer)
  }, [current, error, load, tick])

  useEffect(() => {
    if (nextTick !== null && !nextLoaded && !error) void load(nextTick)
  }, [error, load, nextLoaded, nextTick])

  useEffect(() => {
    if (!playing || !frame) return
    if (tick >= finalTick) {
      setPlaying(false)
      return
    }
    if (!following) return
    const timer = setTimeout(() => setTick(tick + 1), replayTickMilliseconds / speed)
    return () => clearTimeout(timer)
  }, [finalTick, following, frame, playing, speed, tick])

  const clamp = useCallback((target: number) => Math.max(0, Math.min(finalTick, Math.round(target))), [finalTick])
  return {
    tick, frame, playing, speed, error,
    buffering: playing && frame !== null && tick < finalTick && !following,
    play: () => {
      setTick((value) => (value >= finalTick ? 0 : value))
      setPlaying(true)
    },
    pause: () => setPlaying(false),
    seek: (target: number) => setTick(clamp(target)),
    step: (offset: number) => {
      setPlaying(false)
      setTick((value) => clamp(value + offset))
    },
    setSpeed,
    retry: () => setError(''),
  }
}
