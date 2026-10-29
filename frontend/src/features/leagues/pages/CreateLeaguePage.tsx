// League setup for Admins: maps, timing, and 8 or 16 entrants (faction, group and
// controller), with live estimates of battles, duration and model decisions. With
// ?from=<league id> it starts from a copy of that league's settings and entrants.

import { type SubmitEvent, useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { listModelProfiles, listScenarios, type ScenarioSummary } from '../../battles/api/battles'
import { ControllerSelect, controllerInput, resolveSelection, type ModelSelection } from '../../battles/components/ControllerSelect'
import { listFactions, type Faction } from '../../factions/api/factions'
import type { ModelProfile } from '../../../shared/domain/model-profile'
import { createLeague, getLeague, LeagueApiError, listLeagues, type GroupLetter, type League, type LeagueScheduling } from '../api/leagues'
import { durationSeconds, estimateLeague, groupLetters } from '../presentation/leagues'

type LoadState = 'loading' | 'ready' | 'error'
/** slot is the entrant's position, which is also its index in the created league. */
interface EntrantDraft { slot: number; factionId: string; group: GroupLetter; controller: string; selection: ModelSelection }
const defaultScenarioID = 'frontier-siege'
const maxMaps = 5
// Default league battles stay well under the one-hour battle timeout for slow models.
const defaultTicks = 500
const tickDurations = ['250ms', '500ms', '1s', '2s']
const decisionIntervals = ['5', '10', '20', '50']

// Changing the entrant count re-deals every row across the groups in entry order, so
// groups stay at four; existing rows keep their faction and controller.
function draftEntrants(size: 8 | 16, factions: Faction[], previous: EntrantDraft[]): EntrantDraft[] {
  const groups = size / 4
  return Array.from({ length: size }, (_, index) => ({
    ...(previous[index] ?? { slot: index, factionId: factions[index]?.id ?? '', controller: 'scripted', selection: {} }),
    group: groupLetters[index % groups],
  }))
}

interface SetupDraft { name: string; size: 8 | 16; maps: string[]; tickCount: number; tickDuration: string; decisionInterval: string; entrants: EntrantDraft[] }

function defaultSetup(factions: Faction[], scenarios: ScenarioSummary[]): SetupDraft {
  const first = scenarios.find(scenario => scenario.id === defaultScenarioID) ?? scenarios[0]
  return { name: '', size: 8, maps: first ? [first.id] : [], tickCount: defaultTicks, tickDuration: '', decisionInterval: '', entrants: draftEntrants(8, factions, []) }
}

// A copied league keeps references that no longer resolve visible: a deleted faction
// or map is left unchosen and a missing model profile stays selected, so the
// blockers report them instead of the copy silently substituting another.
function copiedSetup(league: League, factions: Faction[], scenarios: ScenarioSummary[]): SetupDraft {
  const entrants = [...league.entrants].sort((left, right) => left.index - right.index)
  return {
    name: league.name, size: entrants.length === 16 ? 16 : 8,
    maps: league.settings.scenarioIds.filter(id => scenarios.some(scenario => scenario.id === id)),
    tickCount: league.settings.tickCount, tickDuration: league.settings.tickDuration ?? '',
    decisionInterval: league.settings.decisionIntervalTicks ? String(league.settings.decisionIntervalTicks) : '',
    entrants: entrants.map(({ group, factionId, controller }, slot): EntrantDraft => ({
      slot, group, factionId: factions.some(faction => faction.id === factionId) ? factionId : '',
      ...(controller.type === 'model'
        ? { controller: controller.profileId ?? '', selection: { modelIdentifier: controller.modelIdentifier, reasoningEffort: controller.reasoningEffort } }
        : { controller: 'scripted', selection: {} }),
    })),
  }
}

/** Keeps a copied value that is not among the standard choices selectable. */
function withCurrent(choices: string[], current: string) {
  return current && !choices.includes(current) ? [...choices, current] : choices
}

/** Lists what blocks submission, in the order a person would fix it. */
function problems(name: string, entrants: EntrantDraft[], maps: string[], ticksValid: boolean, profiles: ModelProfile[]) {
  const found: string[] = []
  if (!name.trim() || name.trim().length > 80) found.push('Name the league (up to 80 characters).')
  if (maps.length === 0) found.push('Choose at least one map.')
  if (!ticksValid) found.push('Set a tick limit every chosen map allows.')
  const factionIDs = entrants.map(entrant => entrant.factionId)
  if (factionIDs.some(id => !id) || new Set(factionIDs).size !== factionIDs.length) found.push('Give every entrant a different faction.')
  const groupSizes = groupLetters.map(letter => entrants.filter(entrant => entrant.group === letter).length)
  if (groupSizes.some(count => count !== 0 && count !== 4)) found.push('Put exactly four entrants in each group.')
  if (entrants.some(entrant => entrant.controller !== 'scripted' && !profiles.some(profile => profile.id === entrant.controller && profile.available))) found.push('Choose an available controller for every entrant.')
  return found
}

export function CreateLeaguePage() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const sourceID = params.get('from') ?? ''
  const [loadState, setLoadState] = useState<LoadState>('loading')
  const [loadVersion, setLoadVersion] = useState(0)
  const [factions, setFactions] = useState<Faction[]>([])
  const [scenarios, setScenarios] = useState<ScenarioSummary[]>([])
  const [profiles, setProfiles] = useState<ModelProfile[]>([])
  const [leagueID] = useState(() => crypto.randomUUID())
  const [name, setName] = useState('')
  const [size, setSize] = useState<8 | 16>(8)
  const [maps, setMaps] = useState<string[]>([])
  const [tickCount, setTickCount] = useState<number | ''>(defaultTicks)
  const [tickDuration, setTickDuration] = useState('')
  const [decisionInterval, setDecisionInterval] = useState('')
  const [entrants, setEntrants] = useState<EntrantDraft[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState('')
  const [scheduling, setScheduling] = useState<LeagueScheduling>()
  const [source, setSource] = useState<League>()

  useEffect(() => {
    const controller = new AbortController()
    setLoadState('loading')
    Promise.all([listFactions(controller.signal), listScenarios(controller.signal), listModelProfiles(controller.signal).catch(() => []),
      listLeagues(controller.signal).then(list => list.scheduling).catch(() => undefined),
      sourceID ? getLeague(sourceID, controller.signal) : undefined])
      .then(([loadedFactions, loadedScenarios, loadedProfiles, loadedScheduling, loadedSource]) => {
        if (controller.signal.aborted) return
        setScheduling(loadedScheduling)
        setFactions(loadedFactions)
        setScenarios(loadedScenarios)
        setProfiles(loadedProfiles)
        setSource(loadedSource)
        const setup = loadedSource ? copiedSetup(loadedSource, loadedFactions, loadedScenarios) : defaultSetup(loadedFactions, loadedScenarios)
        setName(setup.name)
        setSize(setup.size)
        setMaps(setup.maps)
        setTickCount(setup.tickCount)
        setTickDuration(setup.tickDuration)
        setDecisionInterval(setup.decisionInterval)
        setEntrants(setup.entrants)
        setLoadState('ready')
      })
      .catch(() => { if (!controller.signal.aborted) setLoadState('error') })
    return () => controller.abort()
  }, [loadVersion, sourceID])

  const chosen = scenarios.filter(scenario => maps.includes(scenario.id))
  const maximumTicks = chosen.length ? Math.min(...chosen.map(scenario => scenario.maximumTicks)) : 0
  const ticksValid = typeof tickCount === 'number' && Number.isInteger(tickCount) && tickCount >= 1 && tickCount <= maximumTicks
  const blockers = problems(name, entrants, maps, ticksValid, profiles)
  const modelControllers = entrants.filter(entrant => entrant.controller !== 'scripted')
  const estimate = estimateLeague({
    entrants: size, modelEntrants: modelControllers.length,
    cliEntrants: modelControllers.filter(entrant => profiles.find(profile => profile.id === entrant.controller)?.runtime === 'cli').length,
    ticks: typeof tickCount === 'number' ? tickCount : 0, tickSeconds: durationSeconds(tickDuration || chosen[0]?.tickDuration) ?? 1,
    interval: Number(decisionInterval) || chosen[0]?.decisionIntervalTicks || 10,
  }, scheduling)

  function changeSize(next: 8 | 16) {
    setSize(next)
    setEntrants(current => draftEntrants(next, factions, current))
  }
  function updateEntrant(index: number, change: Partial<EntrantDraft>) {
    setEntrants(current => current.map((entrant, position) => position === index ? { ...entrant, ...change } : entrant))
  }

  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (blockers.length > 0) return
    setSubmitting(true)
    setSubmitError('')
    try {
      const league = await createLeague({
        id: leagueID, name: name.trim(), scenarioIds: maps, tickCount: Number(tickCount),
        ...(tickDuration && { tickDuration }), ...(decisionInterval && { decisionIntervalTicks: Number(decisionInterval) }),
        entrants: entrants.map(entrant => ({ factionId: entrant.factionId, group: entrant.group,
          controller: controllerInput(entrant.controller, resolveSelection(profiles.find(profile => profile.id === entrant.controller), entrant.selection)) })),
      })
      navigate(`/leagues/${league.id}`)
    } catch (error) {
      setSubmitting(false)
      setSubmitError(error instanceof LeagueApiError ? error.message : 'The league could not be started.')
    }
  }

  return (
    <>
      <section className="page-header page-header--battle">
        <Link className="back-link" to="/leagues">← LEAGUES</Link>
        <div className="hero__eyebrow"><span>05</span> LEAGUE SETUP</div>
        <h1>Start a league</h1>
        <p>Entrants play every group rival twice, once from each side, then the top two of each group play a two-leg crossover knockout. Battles run one at a time in the background.</p>
        {source && <p>Settings and entrants are copied from <Link to={`/leagues/${encodeURIComponent(source.id)}`}>{source.name}</Link>. Change anything before starting; that league is unchanged.</p>}
      </section>
      {loadState === 'loading' && <section className="directory-state" aria-busy="true"><span className="loader" aria-hidden="true" /><h2>Loading factions, maps and models…</h2></section>}
      {loadState === 'error' && <section className="directory-state directory-state--error" role="alert">
        <h2>League setup unavailable</h2><p>{sourceID ? 'Factions, maps or the league to copy could not be loaded.' : 'Factions or maps could not be loaded.'}</p>
        <button className="compact-action" type="button" onClick={() => setLoadVersion(value => value + 1)}>RETRY</button>
      </section>}
      {loadState === 'ready' && factions.length < 8 && <section className="directory-state">
        <h2>Eight factions are required</h2><p>A league needs a different faction for each entrant. Register at least eight factions first.</p>
        <Link className="compact-action" to="/factions/new">CREATE A FACTION →</Link>
      </section>}
      {loadState === 'ready' && factions.length >= 8 && <form className="league-form" onSubmit={submit} noValidate>
        <div className="form-panel league-form__settings">
          <div className="section-label"><span>LEAGUE SETTINGS</span><span>{size} ENTRANTS</span></div>
          <div className="battle-form__field">
            <label htmlFor="league-name">League name</label>
            <input id="league-name" type="text" value={name} maxLength={80} onChange={event => setName(event.target.value)} />
          </div>
          <fieldset className="league-form__choice">
            <legend>Entrants</legend>
            {([8, 16] as const).map(option => <label key={option}><input type="radio" name="league-size" checked={size === option} disabled={option === 16 && factions.length < 16} onChange={() => changeSize(option)} />{option} entrants · {option / 4} groups</label>)}
          </fieldset>
          <MapChoices scenarios={scenarios} maps={maps} onChange={setMaps} />
          <TimingFields chosen={chosen} maximumTicks={maximumTicks} ticksValid={ticksValid} tickCount={tickCount} onTickCount={setTickCount}
            tickDuration={tickDuration} onTickDuration={setTickDuration} decisionInterval={decisionInterval} onDecisionInterval={setDecisionInterval} />
          <EstimatePanel estimate={estimate} />
        </div>
        <div className="form-panel league-form__entrants">
          <div className="section-label"><span>ENTRANTS</span><span>{size / 4} GROUPS OF FOUR</span></div>
          <ol className="league-entrants">
            {entrants.map(entrant => <EntrantRow key={entrant.slot} index={entrant.slot} entrant={entrant} groups={size / 4} factions={factions} profiles={profiles}
              takenFactions={entrants.filter(other => other.slot !== entrant.slot).map(other => other.factionId)} onChange={change => updateEntrant(entrant.slot, change)} />)}
          </ol>
          {blockers.length > 0 && <ul className="field-error league-form__blockers">{blockers.map(blocker => <li key={blocker}>{blocker}</li>)}</ul>}
          <button className="submit-button" type="submit" disabled={blockers.length > 0 || submitting}>
            <span>{submitting ? 'STARTING LEAGUE…' : 'START LEAGUE'}</span><span aria-hidden="true">→</span>
          </button>
          <div className="form-message" aria-live="polite">{submitError && <p className="form-message--error">{submitError}</p>}</div>
        </div>
      </form>}
    </>
  )
}

function MapChoices({ scenarios, maps, onChange }: Readonly<{ scenarios: ScenarioSummary[]; maps: string[]; onChange(maps: string[]): void }>) {
  return (
    <fieldset className="league-form__choice">
      <legend>Maps</legend>
      <p className="field-hint">Group pairings rotate through the chosen maps; both legs of a pairing use the same map.</p>
      {scenarios.map(scenario => <label key={scenario.id}>
        <input type="checkbox" checked={maps.includes(scenario.id)} disabled={!maps.includes(scenario.id) && maps.length >= maxMaps}
          onChange={event => onChange(event.target.checked ? [...maps, scenario.id] : maps.filter(id => id !== scenario.id))} />
        {scenario.id} · {scenario.mapWidth}×{scenario.mapHeight}
      </label>)}
    </fieldset>
  )
}

function TimingFields(props: Readonly<{
  chosen: ScenarioSummary[]; maximumTicks: number; ticksValid: boolean; tickCount: number | ''; onTickCount(value: number | ''): void
  tickDuration: string; onTickDuration(value: string): void; decisionInterval: string; onDecisionInterval(value: string): void
}>) {
  const first = props.chosen[0]
  return (
    <div className="battle-timing-grid">
      <div className="battle-form__field">
        <label htmlFor="league-ticks">Maximum ticks per battle</label>
        <input id="league-ticks" type="number" min="1" max={props.maximumTicks} value={props.tickCount} aria-invalid={!props.ticksValid}
          onChange={event => props.onTickCount(event.currentTarget.value === '' ? '' : event.currentTarget.valueAsNumber)} />
        {!props.ticksValid && props.maximumTicks > 0 && <p className="field-error">Enter a whole number from 1 to {props.maximumTicks}.</p>}
      </div>
      <div className="battle-form__field">
        <label htmlFor="league-tick-duration">Tick duration</label>
        <select id="league-tick-duration" value={props.tickDuration} onChange={event => props.onTickDuration(event.target.value)}>
          <option value="">Map default ({first?.tickDuration ?? '—'})</option>
          {withCurrent(tickDurations, props.tickDuration).map(value => <option key={value} value={value}>{value}</option>)}
        </select>
      </div>
      <div className="battle-form__field">
        <label htmlFor="league-interval">Decision interval</label>
        <select id="league-interval" value={props.decisionInterval} onChange={event => props.onDecisionInterval(event.target.value)}>
          <option value="">Map default ({first?.decisionIntervalTicks ?? '—'} ticks)</option>
          {withCurrent(decisionIntervals, props.decisionInterval).map(ticks => <option key={ticks} value={ticks}>Every {ticks} ticks</option>)}
        </select>
      </div>
    </div>
  )
}

function atOnce({ low, high }: { low: number; high: number }) {
  if (high === 1) return 'League battles run one at a time.'
  if (low === high) return `Up to ${high} league battles run at once.`
  return `Up to ${high} league battles run at once, or ${low} when every battle has two CLI models.`
}

function EstimatePanel({ estimate }: Readonly<{ estimate: ReturnType<typeof estimateLeague> }>) {
  const hours = (value: number) => value < 1 ? `${Math.round(value * 60)} min` : `${value.toFixed(1)} h`
  const minutes = (value: number) => `${Math.round(value)} min`
  const range = (low: string, high: string) => low === high ? low : `${low}–${high}`
  return (
    <section className="league-estimate" aria-label="League estimate">
      <dl>
        <div><dt>Battles</dt><dd>{estimate.battles}</dd></div>
        <div><dt>Battle length</dt><dd>{range(minutes(estimate.battleMinutes.low), minutes(estimate.battleMinutes.high))}</dd></div>
        <div><dt>League length</dt><dd>{range(hours(estimate.leagueHours.low), hours(estimate.leagueHours.high))}</dd></div>
        <div><dt>Model decisions</dt><dd>up to {estimate.modelDecisions.toLocaleString()}</dd></div>
      </dl>
      <p className="field-hint">{atOnce(estimate.battlesAtOnce)} League battles have no per-battle decision budget, and battles that end early use fewer decisions.</p>
      {estimate.battleMinutes.high > 45 && <p className="field-error">Slow models may approach the one-hour battle timeout. Use fewer ticks or a longer decision interval.</p>}
    </section>
  )
}

function EntrantRow({ index, entrant, groups, factions, profiles, takenFactions, onChange }: Readonly<{
  index: number; entrant: EntrantDraft; groups: number; factions: Faction[]; profiles: ModelProfile[]; takenFactions: string[]; onChange(change: Partial<EntrantDraft>): void
}>) {
  const id = `entrant-${index}`
  return (
    <li className="league-entrant">
      <span className="league-entrant__number">{String(index + 1).padStart(2, '0')}</span>
      <div className="battle-form__field">
        <label htmlFor={`${id}-faction`}>Faction</label>
        <select id={`${id}-faction`} value={entrant.factionId} onChange={event => onChange({ factionId: event.target.value })}>
          <option value="">Choose a faction</option>
          {factions.map(faction => <option key={faction.id} value={faction.id} disabled={takenFactions.includes(faction.id)}>{faction.name}</option>)}
        </select>
      </div>
      <div className="battle-form__field">
        <label htmlFor={`${id}-group`}>Group</label>
        <select id={`${id}-group`} value={entrant.group} onChange={event => onChange({ group: event.target.value as GroupLetter })}>
          {groupLetters.slice(0, groups).map(letter => <option key={letter} value={letter}>{letter}</option>)}
        </select>
      </div>
      <div className="battle-form__field">
        <label htmlFor={`${id}-controller`}>Controller</label>
        <ControllerSelect id={`${id}-controller`} value={entrant.controller} profiles={profiles} selection={entrant.selection}
          onChange={value => onChange({ controller: value, selection: {} })} onSelect={selection => onChange({ selection })} />
      </div>
    </li>
  )
}
