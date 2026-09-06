import { useEffect, useState, type SubmitEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { createFaction, listFactions, updateFaction } from '../api/factions'
import { Emblem } from '../../../shared/components/Emblem'

export const doctrineTemplates = {
 Balanced: 'Balance offense, defense, and economic growth. Protect your castle and income, scout before committing, and adapt purchases to visible threats.',
 Aggressive: 'Pressure the enemy early. Favor offensive units and upgrades, exploit exposed income, and commit when a decisive attack is possible. Protect your castle from counterattacks.',
 Defensive: 'Protect the castle and preserve units. Favor durable forces, scout threats, and counterattack when the enemy overextends. Invest surplus gold in sustainable growth.',
 Economic: 'Build a strong economy while maintaining enough defense to survive. Preserve income, invest in economic upgrades, and convert your advantage into a decisive army.',
}

export function CreateFactionPage() {
 const { id } = useParams()
 const editing = Boolean(id)
 const [factionId, setFactionId] = useState(() => crypto.randomUUID())
 const [name, setName] = useState('')
 const [doctrine, setDoctrine] = useState(doctrineTemplates.Balanced)
 const [loading, setLoading] = useState(editing)
 const [loadFailed, setLoadFailed] = useState(false)
 const [status, setStatus] = useState<'idle' | 'submitting' | 'success' | 'error'>('idle')
 const [message, setMessage] = useState('')
 useEffect(() => {
 if (!id) return
 const controller = new AbortController()
 setLoading(true)
 setLoadFailed(false)
 listFactions(controller.signal).then(factions => {
 if (controller.signal.aborted) return
 const faction = factions.find(value => value.id === id)
 if (!faction) throw new Error('Faction not found.')
 setName(faction.name); setDoctrine(faction.doctrine); setLoading(false)
 }).catch(error => {
 if (controller.signal.aborted) return
 setLoading(false); setLoadFailed(true); setStatus('error'); setMessage(error instanceof Error ? error.message : 'Faction could not be loaded.')
 })
 return () => controller.abort()
 }, [id])
 const count = Array.from(doctrine.trim()).length
 const valid = name.trim().length > 0 && count > 0 && count <= 4000 && !loading && !loadFailed
 async function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
 event.preventDefault()
 if (!valid || status === 'submitting') return
 setStatus('submitting'); setMessage('')
 try {
 const faction = { id: id ?? factionId, name: name.trim(), doctrine: doctrine.trim() }
 await (editing ? updateFaction(faction) : createFaction(faction))
 setStatus('success'); setMessage(editing ? `${faction.name} has been updated. Changes apply to future battles.` : `${faction.name} has entered the battlefield.`)
 if (!editing) { setName(''); setFactionId(crypto.randomUUID()) }
 } catch (error) { setStatus('error'); setMessage(error instanceof Error ? error.message : 'An unexpected error occurred. Please try again.') }
 }
 return <>
 <section className="page-header">
 <Link className="back-link" to="/">← MAIN MENU</Link>
 <div className="hero__eyebrow"><span>01</span> FACTION REGISTRY</div>
 <h1>{editing ? 'Edit faction' : 'Create a faction'}</h1>
 <p>Define how your commander thinks. The agent chooses units, upgrades, and tactics in each battle.</p>
 </section>
 <section className="creation-layout" aria-labelledby="creation-heading">
 <div className="form-panel">
 <div className="section-label"><span>STRATEGIC DOCTRINE</span></div>
 <h2 id="creation-heading">Faction identity</h2>
 {loading ? <p>Loading faction…</p> : !loadFailed && <form onSubmit={handleSubmit} noValidate>
 <label htmlFor="faction-name">Faction name</label>
 <div className="name-field"><input id="faction-name" value={name} onChange={event => {setName(event.target.value); setStatus('idle')}} placeholder="e.g. The Verdant Accord" autoComplete="off" /><span>{name.length}</span></div>
 <fieldset><legend>Doctrine templates</legend>{Object.entries(doctrineTemplates).map(([label, text]) => <button type="button" key={label} onClick={() => {setDoctrine(text); setStatus('idle')}}>{label}</button>)}</fieldset>
 <label htmlFor="faction-doctrine">Doctrine</label>
 <textarea id="faction-doctrine" rows={9} value={doctrine} aria-describedby="doctrine-hint" onChange={event => {setDoctrine(event.target.value);setStatus('idle')}} />
 <p id="doctrine-hint" className="field-hint">Describe priorities, risk tolerance, and how to adapt. {count} / 4,000 characters.</p>
 {count > 4000 && <p role="alert">Doctrine must be at most 4,000 characters.</p>}
 <div className="identity-block"><div><span className="identity-block__label">SYSTEM IDENTIFIER</span><code title={id ?? factionId}>{id ?? factionId}</code></div>{!editing && <button type="button" className="icon-button" onClick={() => setFactionId(crypto.randomUUID())} aria-label="Generate a new faction identifier">↻</button>}</div>
 <button className="submit-button" type="submit" disabled={!valid || status === 'submitting'}>{status === 'submitting' ? 'SAVING…' : editing ? 'SAVE FACTION' : 'CREATE FACTION'}</button>
 </form>}
 <div className="form-message" aria-live="polite">{message && <p className={`form-message--${status}`}>{message}</p>}{status === 'success' && <Link className="text-link" to="/factions">View faction registry →</Link>}</div>
 </div>
 <aside className="preview-panel" aria-label="Faction doctrine preview"><div className="preview-panel__grid" /><div className="preview-panel__heading"><span>FACTION PREVIEW</span></div><div className="preview-panel__content"><Emblem seed={name.trim() || 'Your faction'} /><h3>{name.trim() || 'Your faction'}</h3><p style={{whiteSpace:'pre-wrap'}}>{doctrine}</p></div></aside>
 </section>
 </>
}
