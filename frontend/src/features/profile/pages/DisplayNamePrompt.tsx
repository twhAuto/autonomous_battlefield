// Blocking first-sign-in screen: a Player or Tester without a display name chooses one
// before the app opens.

import { DisplayNameForm } from '../components/DisplayNameForm'

interface DisplayNamePromptProps {
  readonly logoutError: string
  readonly onSaved: (name: string) => void
  readonly onLogout: () => void
}

export function DisplayNamePrompt({ logoutError, onSaved, onLogout }: DisplayNamePromptProps) {
  return (
    <main className="login-page">
      <section className="login-card display-name-prompt" aria-labelledby="display-name-title">
        <div className="login-card__mark" aria-hidden="true">O</div>
        <span className="login-card__eyebrow">OrdinalFrontier</span>
        <h1 id="display-name-title">Choose your display name</h1>
        <p>Pick the name other commanders will know you by. You can change it later.</p>
        <DisplayNameForm submitLabel="ENTER COMMAND POST" onSaved={onSaved} />
        {logoutError && <p className="login-card__error" role="alert">{logoutError}</p>}
        <button className="text-button display-name-prompt__logout" type="button" onClick={onLogout}>LOG OUT</button>
      </section>
    </main>
  )
}
