// Shared Vitest setup: installs DOM matchers and cleans rendered React trees after each
// test.

import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterEach } from 'vitest'

// Async queries have their own deadline, independent of Vitest's testTimeout.
// Allow chained fetch/render effects to settle on the shared Jenkins agent.
configure({ asyncUtilTimeout: 5_000 })

afterEach(() => {
  cleanup()
})
