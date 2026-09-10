import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { Identifier } from './Identifier'

it('copies the full identifier and reveals a manual fallback on failure', async () => {
  const user = userEvent.setup()
  const write = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
  const id = '12345678-1234-1234-1234-123456789abc'
  render(<Identifier value={id} label="Faction identifier" />)
  expect(screen.getByText('12345678')).toBeVisible()
  expect(screen.getByText(id)).not.toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Copy faction identifier' }))
  expect(write).toHaveBeenCalledWith(id)
  expect(screen.getByRole('status')).toHaveTextContent('Faction identifier copied.')
  write.mockRejectedValue(new Error('denied'))
  await user.click(screen.getByRole('button', { name: 'Copy faction identifier' }))
  expect(screen.getByText(id)).toBeVisible()
  expect(screen.getByRole('status')).toHaveTextContent('Select and copy')
})
