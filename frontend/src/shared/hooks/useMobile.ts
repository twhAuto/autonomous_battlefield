import { useEffect, useState } from 'react'

/** Keep responsive behavior aligned with the shared CSS breakpoint. */
export function useMobile() {
  const [mobile, setMobile] = useState(() => typeof matchMedia !== 'undefined' && matchMedia('(max-width: 780px)').matches)
  useEffect(() => {
    if (typeof matchMedia === 'undefined') return
    const query = matchMedia('(max-width: 780px)')
    const update = () => setMobile(query.matches)
    update()
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])
  return mobile
}
