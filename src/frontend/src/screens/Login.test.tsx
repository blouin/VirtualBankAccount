import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loadSession } from '../api'
import { err, mockApi, renderApp } from '../test/helpers'
import App from '../App'

const persons = [{ id: 1, name: 'Parent 1' }, { id: 3, name: 'Child 1' }]

describe('Login', () => {
  it('lists the family, logs in with remember and a device label, then stores the session', async () => {
    const calls = mockApi((m, p) => {
      if (p.startsWith('/auth/persons')) return persons
      if (p === '/auth/login') return { token: 'abc', expiresAt: null, person: { id: 3, name: 'Child 1' } }
      if (p === '/accounts') return []
      if (p.startsWith('/requests')) return { items: [], page: 1, pageSize: 1, total: 0 }
      return undefined
    })
    renderApp(<App />)
    await userEvent.type(screen.getByLabelText('Family code'), 'POC')
    await userEvent.click(screen.getByRole('button', { name: 'Go' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Child 1' }))
    await userEvent.type(screen.getByLabelText('Password'), 'secret1')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))
    await waitFor(() => expect(loadSession()?.token).toBe('abc'))
    const login = calls.find((c) => c.path === '/auth/login')!
    expect(login.body).toMatchObject({ familyCode: 'POC', personId: 3, password: 'secret1', remember: true })
    expect((login.body as { device: string }).device).toBeTruthy()
    expect(await screen.findByText('Accounts', { selector: 'h2' })).toBeInTheDocument()
  })

  it('remembers the family code for next time', async () => {
    localStorage.setItem('vbank.family', 'POC')
    mockApi((m, p) => (p.startsWith('/auth/persons') ? persons : undefined))
    renderApp(<App />)
    expect(await screen.findByRole('button', { name: 'Parent 1' })).toBeInTheDocument()
  })

  it('shows a wrong-password error without logging out', async () => {
    localStorage.setItem('vbank.family', 'POC')
    mockApi((m, p) => (p === '/auth/login' ? err(401, 'invalid credentials') : persons))
    renderApp(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Parent 1' }))
    await userEvent.type(screen.getByLabelText('Password'), 'nope')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))
    expect(await screen.findByText('invalid credentials')).toBeInTheDocument()
  })

  it('shows the wait time when rate limited', async () => {
    localStorage.setItem('vbank.family', 'POC')
    mockApi((m, p) => (p === '/auth/login' ? err(429, 'too many', { 'Retry-After': '300' }) : persons))
    renderApp(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Parent 1' }))
    await userEvent.type(screen.getByLabelText('Password'), 'x')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))
    expect(await screen.findByText('Too many attempts. Try again in 5 min.')).toBeInTheDocument()
  })

  it('shows a message for a family with nobody in it', async () => {
    localStorage.setItem('vbank.family', 'NOPE')
    mockApi(() => [])
    renderApp(<App />)
    expect(await screen.findByText('No one found for this family code.')).toBeInTheDocument()
  })
})
