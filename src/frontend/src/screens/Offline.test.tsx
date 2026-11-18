import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import App from '../App'
import { loadCachedAccounts, saveCachedAccounts, type Balance } from '../api'
import { loggedIn, mockApi, renderApp } from '../test/helpers'
import { Accounts } from './Accounts'
import { Requests } from './Requests'
import { Settings } from './Settings'

const sampleAccounts: Balance[] = [
  {
    accountId: 1,
    name: 'Child 1',
    role: 'manager',
    balance: 2500,
    pending: 500,
    total: 3000,
    dailyFunding: 50,
    skippedToday: false,
    closedAt: null,
  },
  {
    accountId: 2,
    name: 'Child 2',
    role: 'user',
    balance: 1500,
    pending: 0,
    total: 1500,
    dailyFunding: 0,
    skippedToday: false,
    closedAt: null,
  },
]

describe('Offline mode', () => {
  beforeEach(() => {
    localStorage.clear()
    loggedIn()
  })

  it('displays the offline banner on top when offline', async () => {
    mockApi((m, p) => {
      if (p === '/accounts') return sampleAccounts
      return { items: [], page: 1, pageSize: 1, total: 0 }
    })

    renderApp(<App />, '/', { isOffline: true })

    const banner = await screen.findByRole('status')
    expect(banner).toHaveTextContent(/offline mode — showing latest available balances/i)
  })

  it('does not display the offline banner when online', async () => {
    mockApi((m, p) => {
      if (p === '/accounts') return sampleAccounts
      if (p.startsWith('/requests')) return { items: [], page: 1, pageSize: 1, total: 0 }
      return undefined
    })

    renderApp(<App />, '/', { isOffline: false })

    await screen.findByRole('group', { name: 'Child 1' })
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('displays cached accounts and total balance when offline', async () => {
    saveCachedAccounts(sampleAccounts)

    renderApp(<Accounts />, '/', { isOffline: true })

    // Total balance: $25.00 + $15.00 = $40.00
    expect(await screen.findByText(/total balance:/i)).toBeInTheDocument()
    expect(screen.getByText('$40.00')).toBeInTheDocument()

    // Accounts are displayed
    expect(screen.getByRole('group', { name: 'Child 1' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Child 2' })).toBeInTheDocument()
  })

  it('disables expanding an account to show transactions and action buttons when offline', async () => {
    saveCachedAccounts(sampleAccounts)

    renderApp(<Accounts />, '/', { isOffline: true })

    const card = await screen.findByRole('group', { name: 'Child 1' })
    const headerBtn = within(card).getByRole('button', { name: 'Child 1' })

    expect(headerBtn).toHaveAttribute('aria-disabled', 'true')
    // Click should not open
    await userEvent.click(headerBtn)

    expect(within(card).queryByRole('button', { name: 'Withdraw' })).not.toBeInTheDocument()
    expect(within(card).queryByRole('button', { name: 'Deposit' })).not.toBeInTheDocument()
    expect(within(card).queryByText('History')).not.toBeInTheDocument()
  })

  it('disables daily funding toggle in offline mode', async () => {
    saveCachedAccounts(sampleAccounts)

    renderApp(<Accounts />, '/', { isOffline: true })

    const card = await screen.findByRole('group', { name: 'Child 1' })
    const toggleBtn = within(card).getByRole('button', { name: 'Turn off today' })

    expect(toggleBtn).toBeDisabled()
  })

  it('shows an informative offline message on the Requests screen without fetching', async () => {
    const calls = mockApi(() => undefined)

    renderApp(<Requests />, '/requests', { isOffline: true })

    expect(await screen.findByText(/requests are unavailable in offline mode/i)).toBeInTheDocument()
    // No request calls were made
    expect(calls.filter((c) => c.path.startsWith('/requests'))).toHaveLength(0)
  })

  it('hides pending badge in the navigation bar when offline', async () => {
    mockApi((m, p) => {
      if (p === '/accounts') return sampleAccounts
      if (p.startsWith('/requests')) return { items: [{ id: 10 }], page: 1, pageSize: 1, total: 3 }
      return undefined
    })

    renderApp(<App />, '/', { isOffline: true })

    await screen.findByRole('group', { name: 'Child 1' })
    // No circle badge with count should be displayed
    expect(screen.queryByText('3')).not.toBeInTheDocument()
  })

  it('disables the danger zone in settings when offline', async () => {
    renderApp(<Settings />, '/settings', { isOffline: true })

    expect(await screen.findByText('Danger zone')).toBeInTheDocument()
    expect(screen.getByText(/password change and session management are unavailable in offline mode/i)).toBeInTheDocument()

    const dangerBtn = screen.getByRole('button', { name: /danger zone/i })
    expect(dangerBtn).toHaveAttribute('aria-disabled', 'true')

    await userEvent.click(dangerBtn)
    expect(screen.queryByLabelText(/new password/i)).not.toBeInTheDocument()
  })

  it('caches accounts when fetched online and purges them on log off', async () => {
    mockApi((m, p) => {
      if (p === '/accounts') return sampleAccounts
      return undefined
    })

    expect(loadCachedAccounts()).toBeNull()

    renderApp(<Accounts />, '/')
    await screen.findByRole('group', { name: 'Child 1' })

    await waitFor(() => {
      expect(loadCachedAccounts()).toEqual(sampleAccounts)
    })

    // Log off in Settings
    renderApp(<Settings />, '/settings')
    const logOffBtn = await screen.findByRole('button', { name: 'Log off' })
    await userEvent.click(logOffBtn)

    await waitFor(() => {
      expect(loadCachedAccounts()).toBeNull()
    })
  })
})
