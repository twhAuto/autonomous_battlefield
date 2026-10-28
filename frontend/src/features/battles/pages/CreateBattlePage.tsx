// Loads faction, scenario, and model choices, validates the battle setup, and navigates to
// the queued battle after creation.

import { type SubmitEvent, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { BattleApiError, createBattle, listScenarios, listModelProfiles, type ScenarioSummary } from '../api/battles'
import type { ModelProfile } from '../../../shared/domain/model-profile'
import { Faction, FactionApiError, listFactions } from '../../factions/api/factions'
import { Emblem } from '../../../shared/components/Emblem'
import { ResponsivePreview } from '../../../shared/components/ResponsivePreview'
import { MapPreview } from '../components/MapPreview'
import { ControllerSelect, controllerInput, resolveSelection, type ModelSelection } from '../components/ControllerSelect'

type LoadState = 'loading' | 'ready' | 'empty' | 'error'
const defaultScenarioID = 'frontier-siege'

function newBattleID() {
  return crypto.randomUUID()
}

export function CreateBattlePage() {
  const navigate = useNavigate()
  const [factions, setFactions] = useState<Faction[]>([])
  const [scenarios, setScenarios] = useState<ScenarioSummary[]>([])
  const [profiles, setProfiles] = useState<ModelProfile[]>([])
  const [profilesError, setProfilesError] = useState(false)
  const [loadState, setLoadState] = useState<LoadState>('loading')
  const [loadMessage, setLoadMessage] = useState('')
  const [loadVersion, setLoadVersion] = useState(0)
  const [battleID] = useState(newBattleID)
  const [scenarioID, setScenarioID] = useState('')
  const [firstFactionID, setFirstFactionID] = useState('')
  const [secondFactionID, setSecondFactionID] = useState('')
  const [firstController, setFirstController] = useState('scripted')
  const [secondController, setSecondController] = useState('scripted')
  const [firstSelection, setFirstSelection] = useState<ModelSelection>({})
  const [secondSelection, setSecondSelection] = useState<ModelSelection>({})
  const [tickDuration, setTickDuration] = useState('')
  const [decisionInterval, setDecisionInterval] = useState('')
  const [tickCount, setTickCount] = useState<number | ''>(500)
  const [submitState, setSubmitState] = useState<'idle' | 'submitting' | 'error'>('idle')
  const [submitMessage, setSubmitMessage] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    setLoadState('loading')
    setLoadMessage('')
    setProfilesError(false)
    Promise.all([
      listFactions(controller.signal),
      listScenarios(controller.signal),
      listModelProfiles(controller.signal).catch(() => {
        if (!controller.signal.aborted) setProfilesError(true)
        return []
      }),
    ])
      .then(([loadedFactions, loadedScenarios, loadedProfiles]) => {
        if (controller.signal.aborted) return
        setFactions(loadedFactions)
        setScenarios(loadedScenarios)
        setProfiles(loadedProfiles)
        if (loadedFactions.length < 2 || loadedScenarios.length === 0) {
          setLoadState('empty')
          return
        }
        setFirstFactionID(loadedFactions[0].id)
        setSecondFactionID(loadedFactions[1].id)
        setScenarioID((loadedScenarios.find(scenario => scenario.id === defaultScenarioID) ?? loadedScenarios[0]).id)
        setLoadState('ready')
      })
      .catch((error) => {
        if (controller.signal.aborted) return
        setLoadState('error')
        setLoadMessage(
          error instanceof BattleApiError || error instanceof FactionApiError
            ? error.message
            : 'Deployment data could not be loaded.',
        )
      })
    return () => controller.abort()
  }, [loadVersion])

  const selectedScenario = useMemo(
    () => scenarios.find((scenario) => scenario.id === scenarioID),
    [scenarioID, scenarios],
  )
  const firstFaction = factions.find((faction) => faction.id === firstFactionID)
  const secondFaction = factions.find((faction) => faction.id === secondFactionID)
  const maximumTicks = selectedScenario?.maximumTicks ?? 500
  const validTicks = typeof tickCount === 'number' && Number.isInteger(tickCount) && tickCount >= 1 && tickCount <= maximumTicks
  const canSubmit = loadState === 'ready'
    && [firstController, secondController].every(value => value === 'scripted' || profiles.some(profile => profile.id === value && profile.available))
    && firstFactionID !== ''
    && secondFactionID !== ''
    && firstFactionID !== secondFactionID
    && scenarioID !== ''
    && validTicks
    && submitState !== 'submitting'

  useEffect(() => {
    if (!selectedScenario) return
    setTickDuration('')
    setDecisionInterval('')
    setFirstFactionID(factions[0]?.id ?? '')
    setSecondFactionID(factions[1]?.id ?? '')
    const scenarioMaximum = selectedScenario.maximumTicks ?? 500
    setTickCount((current) => current === '' ? current : Math.min(current, scenarioMaximum))
  }, [factions, selectedScenario])

  async function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) {
      setSubmitState('error')
      setSubmitMessage(`Select two different factions and a tick limit between 1 and ${maximumTicks}.`)
      return
    }
    setSubmitState('submitting')
    setSubmitMessage('')
    try {
      const battle = await createBattle({
        id: battleID,
        scenarioId: scenarioID,
        participants: [
          { factionId: firstFactionID, controller: controllerInput(firstController, resolveSelection(profiles.find(profile => profile.id === firstController), firstSelection)) },
          { factionId: secondFactionID, controller: controllerInput(secondController, resolveSelection(profiles.find(profile => profile.id === secondController), secondSelection)) },
        ],
        tickCount: Number(tickCount),
        ...(tickDuration && { tickDuration }),
        ...(decisionInterval && { decisionIntervalTicks: Number(decisionInterval) }),
      })
      navigate(`/battles/${battle.id}`)
    } catch (error) {
      setSubmitState('error')
      setSubmitMessage(error instanceof BattleApiError ? error.message : 'The battle could not be queued.')
    }
  }

  return (
    <>
      <section className="page-header page-header--battle">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow"><span>03</span> BATTLE DEPLOYMENT</div>
        <h1>Stage a battle</h1>
        <p>Select two registered factions, choose a map, and set the battle timing.</p>
      </section>

      {loadState === 'loading' && (
        <section className="directory-state" aria-live="polite" aria-busy="true">
          <span className="loader" aria-hidden="true" />
          <h2>Preparing the battlefield</h2>
          <p>Loading factions and validated combat scenarios…</p>
        </section>
      )}

      {loadState === 'error' && (
        <section className="directory-state directory-state--error" role="alert">
          <span className="directory-state__code">DEPLOYMENT LINK INTERRUPTED</span>
          <h2>Battlefield unavailable</h2>
          <p>{loadMessage}</p>
          <button className="compact-action" type="button" onClick={() => setLoadVersion((value) => value + 1)}>RETRY CONNECTION</button>
        </section>
      )}

      {loadState === 'empty' && (
        <section className="directory-state">
          <span className="directory-state__code">DEPLOYMENT REQUIREMENTS INCOMPLETE</span>
          <h2>{factions.length < 2 ? 'Two factions are required' : 'No scenarios are available'}</h2>
          <p>{factions.length < 2
            ? 'Register at least two factions before staging an engagement.'
            : 'Add a valid scenario to the server catalog and restart the API.'}</p>
          {factions.length < 2 && <Link className="compact-action" to="/factions/new">CREATE A FACTION →</Link>}
        </section>
      )}

      {loadState === 'ready' && (
        <section className="battle-creation-layout">
          <div className="form-panel battle-form-panel">
            <div className="section-label"><span>ENGAGEMENT PARAMETERS</span><span>READY</span></div>
            <form className="battle-form" onSubmit={handleSubmit} noValidate>
              <div className="battle-form__field">
                <label htmlFor="scenario">Combat map</label>
                <select id="scenario" value={scenarioID} onChange={(event) => setScenarioID(event.target.value)}>
                  {scenarios.map((scenario) => <option key={scenario.id} value={scenario.id}>{scenario.id} · {scenario.mapWidth}×{scenario.mapHeight}</option>)}
                </select>
              </div>

              <div className="combatants-grid">
                <div className="battle-form__field">
                  <label htmlFor="first-faction">Slot one</label>
                  <select id="first-faction" value={firstFactionID} onChange={(event) => setFirstFactionID(event.target.value)}>
                    {factions.map((faction) => <option key={faction.id} value={faction.id} disabled={faction.id === secondFactionID}>{faction.name}</option>)}
                  </select>
                  <label htmlFor="first-controller">Controller</label>
                  <ControllerSelect id="first-controller" value={firstController} onChange={value => { setFirstController(value); setFirstSelection({}) }} profiles={profiles} selection={firstSelection} onSelect={setFirstSelection} />
                </div>
                <span className="versus-mark" aria-hidden="true">VS</span>
                <div className="battle-form__field">
                  <label htmlFor="second-faction">Slot two</label>
                  <select id="second-faction" value={secondFactionID} onChange={(event) => setSecondFactionID(event.target.value)}>
                    {factions.map((faction) => <option key={faction.id} value={faction.id} disabled={faction.id === firstFactionID}>{faction.name}</option>)}
                  </select>
                  <label htmlFor="second-controller">Controller</label>
                  <ControllerSelect id="second-controller" value={secondController} onChange={value => { setSecondController(value); setSecondSelection({}) }} profiles={profiles} selection={secondSelection} onSelect={setSecondSelection} />
                </div>
              </div>
              {firstFactionID === secondFactionID && <p className="field-error" role="alert">Choose two different factions.</p>}
              {factions.length < 2 && <p className="field-error" role="alert">Create two factions before deployment.</p>}
              {profilesError && <p className="field-error" role="alert">AI controllers could not be loaded. Reload the page to try again, or choose Scripted.</p>}
              <div className="battle-timing-grid">
                <div className="battle-form__field">
                  <label htmlFor="tick-duration">Tick duration</label>
                  <select id="tick-duration" value={tickDuration} onChange={(event) => setTickDuration(event.target.value)} aria-describedby="tick-duration-hint">
                    <option value="">Map default ({selectedScenario?.tickDuration})</option>
                    <option value="250ms">0.25 seconds</option>
                    <option value="500ms">0.5 seconds</option>
                    <option value="1s">1 second</option>
                    <option value="2s">2 seconds</option>
                    <option value="5s">5 seconds</option>
                  </select>
                  <p id="tick-duration-hint" className="field-hint">Time between simulation ticks. AI decisions may add waiting time.</p>
                </div>
                <div className="battle-form__field">
                  <label htmlFor="decision-interval">Decision interval</label>
                  <select id="decision-interval" value={decisionInterval} onChange={(event) => setDecisionInterval(event.target.value)} aria-describedby="decision-interval-hint">
                    <option value="">Map default ({selectedScenario?.decisionIntervalTicks} ticks)</option>
                    {[1, 5, 10, 20, 50].map((ticks) => <option key={ticks} value={ticks}>{ticks === 1 ? 'Every tick' : `Every ${ticks} ticks`}</option>)}
                  </select>
                  <p id="decision-interval-hint" className="field-hint">How often controllers choose a new plan after the opening.</p>
                </div>
              </div>

              <div className="battle-form__field">
                <label htmlFor="tick-count">Maximum ticks</label>
                <input id="tick-count" type="number" min="1" max={maximumTicks} value={tickCount} onChange={(event) => setTickCount(event.currentTarget.value === '' ? '' : event.currentTarget.valueAsNumber)} aria-invalid={!validTicks} />
                <p className="field-hint">The battle ends earlier if one faction is eliminated.</p>
                {!validTicks && <p className="field-error" role="alert">Enter a whole number from 1 to {maximumTicks}.</p>}
              </div>

              <button className="submit-button" type="submit" disabled={!canSubmit}>
                <span>{submitState === 'submitting' ? 'QUEUING BATTLE…' : 'DEPLOY COMBATANTS'}</span>
                <span aria-hidden="true">→</span>
              </button>
              <div className="form-message" aria-live="polite">
                {submitMessage && <p className="form-message--error">{submitMessage}</p>}
              </div>
            </form>
          </div>

          <ResponsivePreview label="Preview combatants"><aside className="battle-preview" aria-label="Selected combatants">
            <div className="preview-panel__grid" />
            <div className="preview-panel__heading"><span>COMBATANT PREVIEW</span><span>ARMED</span></div>
            {selectedScenario?.map && <MapPreview map={selectedScenario.map} scenarioId={selectedScenario.id} factionIds={[firstFactionID, secondFactionID]} />}
            <div className="battle-preview__combatants">
              <Combatant faction={firstFaction} slot="01" />
              <div className="battle-preview__axis"><span />VERSUS<span /></div>
              <Combatant faction={secondFaction} slot="02" />
            </div>
          </aside></ResponsivePreview>
        </section>
      )}
    </>
  )
}

function Combatant({ faction, slot }: Readonly<{ faction?: Faction; slot: string }>) {
  return (
    <div className="battle-preview__faction">
      {faction && <Emblem seed={faction.name} emblem={faction.emblem} />}
      <span>SLOT {slot}</span>
      <h2>{faction?.name ?? 'Unassigned'}</h2>
    </div>
  )
}
