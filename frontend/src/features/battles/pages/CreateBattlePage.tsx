// Loads faction, scenario, and model choices, validates the battle setup, and navigates to
// the queued battle after creation.

import { type SubmitEvent, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  BattleApiError,
  createBattle,
  listScenarios,
  listModelProfiles,
  ModelProfile,
  ScenarioSummary,
} from '../api/battles'
import { Faction, FactionApiError, listFactions } from '../../factions/api/factions'
import { Emblem } from '../../../shared/components/Emblem'

type LoadState = 'loading' | 'ready' | 'empty' | 'error'

function newBattleID() {
  return crypto.randomUUID()
}

export function CreateBattlePage() {
  const navigate = useNavigate()
  const [factions, setFactions] = useState<Faction[]>([])
  const [scenarios, setScenarios] = useState<ScenarioSummary[]>([])
  const [profiles, setProfiles] = useState<ModelProfile[]>([])
  const [loadState, setLoadState] = useState<LoadState>('loading')
  const [loadMessage, setLoadMessage] = useState('')
  const [loadVersion, setLoadVersion] = useState(0)
  const [battleID, setBattleID] = useState(newBattleID)
  const [scenarioID, setScenarioID] = useState('')
  const [firstFactionID, setFirstFactionID] = useState('')
  const [secondFactionID, setSecondFactionID] = useState('')
  const [firstController, setFirstController] = useState('scripted')
  const [secondController, setSecondController] = useState('scripted')
  const [tickCount, setTickCount] = useState<number | ''>(500)
  const [submitState, setSubmitState] = useState<'idle' | 'submitting' | 'error'>('idle')
  const [submitMessage, setSubmitMessage] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    setLoadState('loading')
    setLoadMessage('')
    Promise.all([listFactions(controller.signal), listScenarios(controller.signal), listModelProfiles(controller.signal).catch(() => [])])
      .then(([loadedFactions, loadedScenarios, loadedProfiles]) => {
        setFactions(loadedFactions)
        setScenarios(loadedScenarios)
        setProfiles(loadedProfiles)
        if (loadedFactions.length < 2 || loadedScenarios.length === 0) {
          setLoadState('empty')
          return
        }
        setFirstFactionID(loadedFactions[0].id)
        setSecondFactionID(loadedFactions[1].id)
        setScenarioID(loadedScenarios[0].id)
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
    && firstFactionID !== ''
    && secondFactionID !== ''
    && firstFactionID !== secondFactionID
    && scenarioID !== ''
    && validTicks
    && submitState !== 'submitting'

  useEffect(() => {
    if (!selectedScenario) return
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
          { factionId: firstFactionID, controller: controllerInput(firstController) },
          { factionId: secondFactionID, controller: controllerInput(secondController) },
        ],
        tickCount: Number(tickCount),
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
        <p>Select two registered factions, choose a ruleset, and set the maximum engagement length.</p>
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
                <label htmlFor="scenario">Combat scenario</label>
                <select id="scenario" value={scenarioID} onChange={(event) => setScenarioID(event.target.value)}>
                  {scenarios.map((scenario) => <option key={scenario.id} value={scenario.id}>{scenario.id} · {scenario.mapWidth}×{scenario.mapHeight}</option>)}
                </select>
                {selectedScenario && (
                  <div className="field-hint"><p>Rules v{selectedScenario.rulesVersion} / decisions every {selectedScenario.decisionIntervalTicks} ticks / {selectedScenario.tickDuration} tick pace</p>
                    <p>Units: {selectedScenario.allowedUnits?.join(', ') || 'Scenario roster'}</p>
                    <p>Buildings: {selectedScenario.allowedBuildings?.join(', ') || 'Scenario buildings'}</p>
                    {selectedScenario.id === 'frontier-siege-v2' && <p>Frontier Siege: broad siege approaches and two flanking lanes. Six sites per side; choose up to four buildings, including at most two economy buildings.</p>}
                  </div>
                )}
              </div>

              <div className="combatants-grid">
                <div className="battle-form__field">
                  <label htmlFor="first-faction">Slot one</label>
                  <select id="first-faction" value={firstFactionID} onChange={(event) => setFirstFactionID(event.target.value)}>
                    {factions.map((faction) => <option key={faction.id} value={faction.id} disabled={faction.id === secondFactionID}>{faction.name}</option>)}
                  </select>
                  <label htmlFor="first-controller">Controller</label>
                  <ControllerSelect id="first-controller" value={firstController} onChange={setFirstController} profiles={profiles} />
                </div>
                <span className="versus-mark" aria-hidden="true">VS</span>
                <div className="battle-form__field">
                  <label htmlFor="second-faction">Slot two</label>
                  <select id="second-faction" value={secondFactionID} onChange={(event) => setSecondFactionID(event.target.value)}>
                    {factions.map((faction) => <option key={faction.id} value={faction.id} disabled={faction.id === firstFactionID}>{faction.name}</option>)}
                  </select>
                  <label htmlFor="second-controller">Controller</label>
                  <ControllerSelect id="second-controller" value={secondController} onChange={setSecondController} profiles={profiles} />
                </div>
              </div>
              {firstFactionID === secondFactionID && <p className="field-error" role="alert">Choose two different factions.</p>}
              {factions.length < 2 && <p className="field-error" role="alert">Create two factions before deployment.</p>}

              <div className="battle-form__field">
                <label htmlFor="tick-count">Maximum ticks</label>
                <input id="tick-count" type="number" min="1" max={maximumTicks} value={tickCount} onChange={(event) => setTickCount(event.currentTarget.value === '' ? '' : event.currentTarget.valueAsNumber)} aria-invalid={!validTicks} />
                <p className="field-hint">The battle ends earlier if one faction is eliminated.</p>
                {!validTicks && <p className="field-error" role="alert">Enter a whole number from 1 to {maximumTicks}.</p>}
              </div>

              <div className="identity-block">
                <div>
                  <span className="identity-block__label">BATTLE IDENTIFIER</span>
                  <code title={battleID}>{battleID}</code>
                </div>
                <button type="button" className="icon-button" onClick={() => setBattleID(newBattleID())} aria-label="Generate a new battle identifier">↻</button>
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

          <aside className="battle-preview" aria-label="Selected combatants">
            <div className="preview-panel__grid" />
            <div className="preview-panel__heading"><span>COMBATANT PREVIEW</span><span>ARMED</span></div>
            <div className="battle-preview__combatants">
              <Combatant faction={firstFaction} slot="01" />
              <div className="battle-preview__axis"><span />VERSUS<span /></div>
              <Combatant faction={secondFaction} slot="02" />
            </div>
          </aside>
        </section>
      )}
    </>
  )
}

function controllerInput(value: string): { type: 'scripted' } | { type: 'model'; profileId: string } {
  return value === 'scripted' ? { type: 'scripted' } : { type: 'model', profileId: value }
}

function ControllerSelect({ id, value, onChange, profiles }: Readonly<{ id: string; value: string; onChange: (value: string) => void; profiles: ModelProfile[] }>) {
  return (
    <select id={id} value={value} onChange={(event) => onChange(event.target.value)}>
      <option value="scripted">Scripted · deterministic</option>
      {profiles.map((profile) => (
        <option key={profile.id} value={profile.id} disabled={!profile.available}>
          {profile.displayName} · {profile.provider} · {profile.modelLabel || 'default model'} · {profile.scope}{profile.available ? '' : ' · unavailable'}
        </option>
      ))}
    </select>
  )
}

function Combatant({ faction, slot }: Readonly<{ faction?: Faction; slot: string }>) {
  return (
    <div className="battle-preview__faction">
      {faction && <Emblem seed={faction.name} />}
      <span>SLOT {slot}</span>
      <h2>{faction?.name ?? 'Unassigned'}</h2>
      <code>{faction?.id ?? '—'}</code>
    </div>
  )
}
