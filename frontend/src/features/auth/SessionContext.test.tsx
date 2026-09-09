import {renderHook} from '@testing-library/react'
import {expect,it} from 'vitest'
import {SessionContext,usePermissions} from './SessionContext'
import type {ReactNode} from 'react'

it.each(['player','developer','admin'] as const)('limits %s ownership and inspection',role=>{
 const wrapper=({children}:{children:ReactNode})=><SessionContext.Provider value={{enabled:true,authenticated:true,mode:'cloudflare',actor:{id:'self',role}}}>{children}</SessionContext.Provider>
 const {result}=renderHook(usePermissions,{wrapper})
 expect(result.current.inspect).toBe(role!=='player')
 expect(result.current.canManage('self')).toBe(true)
 expect(result.current.canManage('other')).toBe(role==='admin')
 expect(result.current.canManage('legacy-admin')).toBe(role==='admin')
})
