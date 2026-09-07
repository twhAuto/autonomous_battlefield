// Checks profile creation and editing, doctrine/background Unicode limits, save failures,
// and duplicate-submission prevention.

import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CreateFactionPage, doctrineTemplates } from './CreateFactionPage'
vi.mock('../components/BackgroundGenerator', () => ({ BackgroundGenerator: () => null }))
const id='7f22e693-bacd-46bb-b942-64fa613cd388'
function show(path='/factions/new') {return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/factions/new" element={<CreateFactionPage/>}/><Route path="/factions/:id/edit" element={<CreateFactionPage/>}/></Routes></MemoryRouter>)}
describe('faction doctrine',()=>{
 beforeEach(()=>{vi.stubGlobal('fetch',vi.fn());vi.spyOn(crypto,'randomUUID').mockReturnValue(id)})
 it('creates a generic profile without loading catalogs',async()=>{
 vi.mocked(fetch).mockResolvedValue(new Response(null,{status:201}))
 const user=userEvent.setup();show()
 expect(fetch).not.toHaveBeenCalled()
 await user.type(screen.getByLabelText('Faction name'),'  Northwind  ')
 await user.type(screen.getByLabelText('Background (optional)'),' Old roots ')

 await user.click(screen.getByRole('button',{name:'Economic'}))
 await user.click(screen.getByRole('button',{name:'CREATE FACTION'}))
 expect(fetch).toHaveBeenCalledWith('/v1/factions',expect.objectContaining({body:JSON.stringify({id,name:'Northwind',doctrine:doctrineTemplates.Economic,background:'Old roots'})}))
 expect(await screen.findByText('Northwind has entered the battlefield.')).toBeInTheDocument()
 })
 it('requires nonblank doctrine and counts Unicode characters',()=>{
 show();fireEvent.change(screen.getByLabelText('Faction name'),{target:{value:'A'}})
 fireEvent.change(screen.getByLabelText('Doctrine'),{target:{value:' '}})
 expect(screen.getByRole('button',{name:'CREATE FACTION'})).toBeDisabled()
 fireEvent.change(screen.getByLabelText('Doctrine'),{target:{value:'😀'.repeat(4000)}})
 expect(screen.getByRole('button',{name:'CREATE FACTION'})).toBeEnabled()
 fireEvent.change(screen.getByLabelText('Doctrine'),{target:{value:'😀'.repeat(4001)}})
 expect(screen.getByRole('alert')).toHaveTextContent('4,000')
 expect(screen.getByRole('button',{name:'CREATE FACTION'})).toBeDisabled()
 })
 it('loads and edits an existing profile',async()=>{
 vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({factions:[{id,name:'A',doctrine:'Scout',background:'Old roots'}]}))).mockResolvedValueOnce(new Response(null,{status:204}))
 const user=userEvent.setup();show(`/factions/${id}/edit`)
 expect(screen.getByText('Loading faction…')).toBeInTheDocument()
 await user.clear(await screen.findByLabelText('Doctrine'));await user.type(screen.getByLabelText('Doctrine'),' Defend ')
 expect(screen.getByLabelText('Background (optional)')).toHaveValue('Old roots')
 await user.clear(screen.getByLabelText('Background (optional)'));await user.type(screen.getByLabelText('Background (optional)'),' New history ')
 await user.click(screen.getByRole('button',{name:'SAVE FACTION'}))
 expect(fetch).toHaveBeenLastCalledWith(`/v1/factions/${id}`,expect.objectContaining({method:'PUT',body:JSON.stringify({name:'A',doctrine:'Defend',background:'New history'})}))
 expect(await screen.findByText(/Changes apply to future battles/)).toBeInTheDocument()
 })
 it.each([404,500])('reports an edit loading failure (%s)',async status=>{
 vi.mocked(fetch).mockResolvedValue(new Response(status===404?JSON.stringify({factions:[]}):null,{status:status===404?200:status}))
 show(`/factions/${id}/edit`)
 await waitFor(()=>expect(screen.queryByText('Loading faction…')).not.toBeInTheDocument())
 expect(screen.queryByRole('button',{name:'SAVE FACTION'})).not.toBeInTheDocument()
 })
 it('shows save failures and prevents duplicate submissions',async()=>{
 let resolve!:(response:Response)=>void
 vi.mocked(fetch).mockImplementation(()=>new Promise(r=>{resolve=r}))
 const user=userEvent.setup();show()
 await user.type(screen.getByLabelText('Faction name'),'A');await user.click(screen.getByRole('button',{name:'CREATE FACTION'}))
 expect(screen.getByRole('button',{name:'SAVING…'})).toBeDisabled()
 resolve(new Response(null,{status:409}))
 expect(await screen.findByText(/identifier is already in use/)).toBeInTheDocument()
 })
})

it('validates optional Unicode backgrounds independently of doctrine',()=>{
 show();fireEvent.change(screen.getByLabelText('Faction name'),{target:{value:'A'}})
 const field=screen.getByLabelText('Background (optional)')
 fireEvent.change(field,{target:{value:'😀'.repeat(1000)}})
 expect(screen.getByRole('button',{name:'CREATE FACTION'})).toBeEnabled()
 fireEvent.change(field,{target:{value:'😀'.repeat(1001)}})
 expect(screen.getByRole('alert')).toHaveTextContent('1,000')
 expect(screen.getByRole('button',{name:'CREATE FACTION'})).toBeDisabled()
})
