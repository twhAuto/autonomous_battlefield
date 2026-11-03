// Lets a Player or Tester change the display name shown on their battles and factions.

import { useState } from 'react'
import { Link } from 'react-router-dom'
import { DisplayNameForm } from '../components/DisplayNameForm'

interface ProfilePageProps {
  readonly name: string
  readonly onSaved: (name: string) => void
}

export function ProfilePage({ name, onSaved }: ProfilePageProps) {
  const [saved, setSaved] = useState('')
  return (
    <>
      <section className="page-header page-header--directory">
        <Link className="back-link" to="/">← MAIN MENU</Link>
        <div className="hero__eyebrow">COMMANDER PROFILE</div>
        <div className="directory-title">
          <div>
            <h1>Display name</h1>
            <p>Your battles show “Started by {name}” and your factions “Created by {name}”.</p>
          </div>
        </div>
      </section>
      <section className="form-panel profile-panel">
        <DisplayNameForm initialName={name} submitLabel="SAVE NAME" onSaved={(next) => { setSaved(next); onSaved(next) }} />
        <output className="form-message form-message--success">{saved && `Saved. You are signed in as ${saved}.`}</output>
      </section>
    </>
  )
}
