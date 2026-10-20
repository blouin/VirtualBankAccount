import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loadSession } from '../api'
import { loggedIn, mockApi, renderApp } from '../test/helpers'
import { Requests } from './Requests'
import { Settings } from './Settings'

const req = (o = {}) => ({
  id: 5, accountId: 7, amount: 250, status: 'pending', requestedBy: 3, requesterComment: 'Allowance',
  requestedAt: '2026-10-20T12:00:00Z', ...o,
})
const page = (items: unknown[]) => ({ items, page: 1, pageSize: 20, total: items.length })

function route(role: 'manager' | 'user', extra: (m: string, p: string) => unknown = () => undefined) {
  return (m: string, p: string) => {
    if (p === '/accounts') return [{ accountId: 7, name: 'Child 1', role }]
    if (p.startsWith('/auth/persons')) return [{ id: 3, name: 'Child 1' }]
    if (p.startsWith('/requests') && m === 'GET') return page([req()])
    return extra(m, p)
  }
}

describe('Requests', () => {
  it('lets a manager approve a pending request', async () => {
    loggedIn()
    const calls = mockApi(route('manager'))
    renderApp(<Requests />)
    expect(await screen.findByText(/Child 1 · \+\$2\.50/)).toBeInTheDocument()
    expect(screen.getByText(/Allowance/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.path === '/requests/5/approve')).toBe(true))
  })

  it('approves in one tap without a body', async () => {
    loggedIn()
    const calls = mockApi(route('manager'))
    renderApp(<Requests />)
    await userEvent.click(await screen.findByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(calls.some((c) => c.path === '/requests/5/approve')).toBe(true))
    expect(calls.find((c) => c.path === '/requests/5/approve')!.body).toBeUndefined()
  })

  it('approves with a comment from the overflow menu', async () => {
    loggedIn()
    const calls = mockApi(route('manager'))
    renderApp(<Requests />)
    await userEvent.click(await screen.findByRole('button', { name: 'More approve options' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: /Approve with comment/ }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Comment (optional)'), 'Well done')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(calls.some((c) => c.path === '/requests/5/approve')).toBe(true))
    expect(calls.find((c) => c.path === '/requests/5/approve')!.body).toEqual({ comment: 'Well done' })
  })

  it('lets a manager reject with an optional reason', async () => {
    loggedIn()
    const calls = mockApi(route('manager'))
    renderApp(<Requests />)
    await userEvent.click(await screen.findByRole('button', { name: 'Reject' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Comment (optional)'), 'Too much')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Reject' }))
    await waitFor(() => expect(calls.some((c) => c.path === '/requests/5/reject')).toBe(true))
    expect(calls.find((c) => c.path === '/requests/5/reject')!.body).toEqual({ comment: 'Too much' })
  })

  it('rejects without a body when no reason is given', async () => {
    loggedIn()
    const calls = mockApi(route('manager'))
    renderApp(<Requests />)
    await userEvent.click(await screen.findByRole('button', { name: 'Reject' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Reject' }))
    await waitFor(() => expect(calls.some((c) => c.path === '/requests/5/reject')).toBe(true))
    expect(calls.find((c) => c.path === '/requests/5/reject')!.body).toBeUndefined()
  })

  it('shows the manager note on a decided request', async () => {
    loggedIn('Child 1')
    mockApi((m, p) => (p.startsWith('/requests') ? page([req({ status: 'rejected', decidedBy: 3, deciderComment: 'Too much' })]) : route('user')(m, p)))
    renderApp(<Requests />)
    await userEvent.click(await screen.findByText('All'))
    expect(await screen.findByText('Approver comment: Too much')).toBeInTheDocument()
    expect(screen.getByText('Requester comment: Allowance')).toBeInTheDocument()
    expect(screen.getByText(/^Requested by Child 1 on /)).toBeInTheDocument()
  })

  it('shows a child their own request without decision buttons', async () => {
    loggedIn('Child 1')
    mockApi(route('user'))
    renderApp(<Requests />)
    expect(await screen.findByText(/Child 1 · \+\$2\.50/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reject' })).not.toBeInTheDocument()
  })

  it('shows decided requests with a status badge under "All"', async () => {
    loggedIn()
    mockApi((m, p) => (p.startsWith('/requests') ? page([req({ status: 'approved' })]) : route('manager')(m, p)))
    renderApp(<Requests />)
    await userEvent.click(await screen.findByText('All'))
    expect(await screen.findByText('Approved')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
  })
})

const openDanger = async () => userEvent.click(await screen.findByText('Danger zone'))

describe('Settings', () => {
  const sessions = [
    { id: 1, device: 'Safari iOS', createdAt: '2026-10-01T00:00:00Z', expiresAt: null, current: true },
    { id: 2, device: 'Chrome Android', createdAt: '2026-10-02T00:00:00Z', expiresAt: null, current: false },
  ]

  it('labels the connected person and keeps the danger zone closed until opened', async () => {
    loggedIn()
    const calls = mockApi((m, p) => (p === '/auth/sessions' ? sessions : undefined))
    renderApp(<Settings />)
    expect(await screen.findByText('Connected:')).toBeInTheDocument()
    expect(screen.getByText('Parent 1')).toBeInTheDocument()
    expect(screen.queryByLabelText('Current password')).not.toBeInTheDocument()
    expect(calls.some((c) => c.path === '/auth/sessions')).toBe(false)
    await openDanger()
    expect(await screen.findByLabelText('Current password')).toBeInTheDocument()
    expect(await screen.findByText(/Safari iOS/)).toBeInTheDocument()
  })

  it('lists devices, marks this one, and revokes another', async () => {
    loggedIn()
    const calls = mockApi((m, p) => (p === '/auth/sessions' ? sessions : undefined))
    renderApp(<Settings />)
    await openDanger()
    expect(await screen.findByText(/Safari iOS/)).toHaveTextContent('this device')
    const buttons = screen.getAllByRole('button', { name: 'Revoke' })
    expect(buttons).toHaveLength(1)
    await userEvent.click(buttons[0])
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.path === '/auth/sessions/2')).toBe(true))
  })

  it('changes the password', async () => {
    loggedIn()
    const calls = mockApi((m, p) => (p === '/auth/sessions' ? sessions : undefined))
    renderApp(<Settings />)
    await openDanger()
    await userEvent.type(screen.getByLabelText('Current password'), 'old-pass')
    await userEvent.type(screen.getByLabelText(/New password/), 'new-pass')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Password changed/)).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'PUT')).toMatchObject({ path: '/auth/changePassword', body: { currentPassword: 'old-pass', newPassword: 'new-pass' } })
  })

  it('logs off and forgets the session', async () => {
    loggedIn()
    const calls = mockApi((m, p) => (p === '/auth/sessions' ? sessions : undefined))
    renderApp(<Settings />)
    await userEvent.click(await screen.findByRole('button', { name: 'Log off' }))
    await waitFor(() => expect(loadSession()).toBeNull())
    expect(calls.some((c) => c.method === 'POST' && c.path === '/auth/logoff')).toBe(true)
  })

  it('switches the language to French and remembers it', async () => {
    loggedIn()
    mockApi((m, p) => (p === '/auth/sessions' ? sessions : undefined))
    renderApp(<Settings />)
    await userEvent.click(await screen.findByText('Français'))
    expect(await screen.findByText('Réglages', { selector: 'h2' })).toBeInTheDocument()
    expect(localStorage.getItem('vbank.lang')).toBe('fr')
    expect(within(document.body).getByText('Se déconnecter')).toBeInTheDocument()
  })

  it('saves the chosen color scheme in localStorage', async () => {
    loggedIn()
    mockApi((m, p) => (p === '/auth/sessions' ? sessions : undefined))
    renderApp(<Settings />)
    await userEvent.click(await screen.findByText('Dark'))
    expect(localStorage.getItem('mantine-color-scheme-value')).toBe('dark')
    await userEvent.click(screen.getByText('Light'))
    expect(localStorage.getItem('mantine-color-scheme-value')).toBe('light')
  })
})
