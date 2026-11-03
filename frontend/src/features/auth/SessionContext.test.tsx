import {renderHook} from '@testing-library/react'
import {expect,it} from 'vitest'
import {SessionContext,usePermissions} from './SessionContext'
import type {ReactNode} from 'react'
import type {AuthSession} from './api/auth'

it.each(['player','tester','admin'] as const)('limits %s ownership and inspection',role=>{
 const wrapper=({children}:{children:ReactNode})=><SessionContext.Provider value={{enabled:true,authenticated:true,mode:'cloudflare',actor:{id:'self',role}}}>{children}</SessionContext.Provider>
 const {result}=renderHook(usePermissions,{wrapper})
 expect(result.current.inspect).toBe(role!=='player')
 expect(result.current.canManage('self')).toBe(true)
 expect(result.current.canManage('other')).toBe(role==='admin')
 expect(result.current.canManage('legacy-admin')).toBe(role==='admin')
})

it.each(['player','tester','admin'] as const)('limits only a %s who owns a faction',role=>{
 const wrapper=({children}:{children:ReactNode})=><SessionContext.Provider value={{enabled:true,authenticated:true,mode:'cloudflare',actor:{id:'self',role}}}>{children}</SessionContext.Provider>
 const {result}=renderHook(usePermissions,{wrapper})
 expect(result.current.oneFaction).toBe(role==='player')
 expect(result.current.atFactionLimit([{ownerId:'other'},{ownerId:'self'}])).toBe(role==='player')
 expect(result.current.atFactionLimit([{ownerId:'other'},{}])).toBe(false)
})

it('never limits a session without an actor',()=>{
 const session:AuthSession={enabled:true,authenticated:true,mode:'cloudflare'}
 const wrapper=({children}:{children:ReactNode})=><SessionContext.Provider value={session}>{children}</SessionContext.Provider>
 const {result}=renderHook(usePermissions,{wrapper})
 expect(result.current.atFactionLimit([{}])).toBe(false)
})
