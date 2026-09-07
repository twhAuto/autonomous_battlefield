// Shared Vitest setup: installs DOM matchers and cleans rendered React trees after each
// test.

import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

afterEach(() => {
  cleanup()
})
