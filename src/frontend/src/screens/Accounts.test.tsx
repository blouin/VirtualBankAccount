import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { Balance } from '../api'
import { today } from '../money'
import { err, loggedIn, mockApi, renderApp } from '../test/helpers'
import { Accounts } from './Accounts'

const acc = (o: Partial<Balance> = {}): Balance => ({
  accountId: 7, name: 'Child 1', role: 'manager', balance: 1000, pending: 250, total: 1250,
  dailyFunding: 50, skippedToday: false, closedAt: null, ...o,
})

const page = (items: unknown[] = []) => ({ items, page: 1, pageSize: 20, total: items.length })

function setup(a: Balance, extra: (m: string, p: string, b: unknown) => unknown = () => undefined) {
  loggedIn()
  return mockApi((m, p, b) => {
    if (p === '/accounts') return [a]
    if (p.startsWith('/accounts/7/transactions') && m === 'GET') return page()
    if (p.startsWith('/requests') && m === 'GET') return page()
    return extra(m, p, b)
  })
}

const findCard = () => screen.findByRole('group', { name: 'Child 1' })

async function openCard() {
  const card = await findCard()
  await userEvent.click(within(card).getByRole('button', { name: 'Child 1' }))
  return card
}

describe('Accounts', () => {
  it('lists the account even when it is the only one, with total, posted, pending and funding', async () => {
    setup(acc())
    renderApp(<Accounts />)
    const card = await findCard()
    expect(card).toHaveTextContent('$10.00')
    expect(card).not.toHaveTextContent('$12.50')
    expect(card).not.toHaveTextContent('posted')
    expect(card).not.toHaveTextContent('daily')
    expect(card).toHaveTextContent('+ $2.50 pending')
    expect(card).toHaveTextContent('Funded today')
  })

  it('keeps funding visible while collapsed, and shows actions and history only when expanded', async () => {
    const calls = setup(acc())
    renderApp(<Accounts />)
    const card = await findCard()
    const header = within(card).getByRole('button', { name: 'Child 1' })
    expect(header).toHaveAttribute('aria-expanded', 'false')
    expect(within(card).getByRole('button', { name: 'Turn off today' })).toBeInTheDocument()
    expect(within(card).queryByRole('button', { name: 'Withdraw' })).not.toBeInTheDocument()
    expect(calls.some((c) => c.path.includes('/transactions'))).toBe(false)
    await userEvent.click(header)
    expect(header).toHaveAttribute('aria-expanded', 'true')
    expect(await within(card).findByRole('button', { name: 'Withdraw' })).toBeInTheDocument()
    expect(await within(card).findByText('History')).toBeInTheDocument()
    await userEvent.click(header)
    await waitFor(() => expect(within(card).queryByRole('button', { name: 'Withdraw' })).not.toBeInTheDocument())
  })

  it('hides every funding line when dailyFunding is 0', async () => {
    setup(acc({ dailyFunding: 0 }))
    renderApp(<Accounts />)
    const card = await findCard()
    expect(card).not.toHaveTextContent(/funded today|skipped today/i)
    expect(within(card).queryByRole('button', { name: /turn (on|off) today/i })).not.toBeInTheDocument()
  })

  it('shows "No" and a hint when a manager skipped today, and a manager can turn it back on', async () => {
    const calls = setup(acc({ skippedToday: true }))
    renderApp(<Accounts />)
    const dialog = await findCard()
    expect(await within(dialog).findByText('Skipped today')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Turn on today' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.path === `/accounts/7/funding-skips/${today()}`)).toBe(true))
  })

  it('lets a manager turn off today with PUT', async () => {
    const calls = setup(acc())
    renderApp(<Accounts />)
    const dialog = await findCard()
    expect(await within(dialog).findByText('Funded today')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Turn off today' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT' && c.path === `/accounts/7/funding-skips/${today()}`)).toBe(true))
  })

  it('shows a child the funding label but no toggle, and a request button instead of deposit', async () => {
    setup(acc({ role: 'user' }))
    renderApp(<Accounts />)
    const dialog = await openCard()
    expect(within(dialog).getByRole('img', { name: 'Funded today' })).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: /turn (on|off) today/i })).not.toBeInTheDocument()
    expect(await within(dialog).findByRole('button', { name: 'Request deposit' })).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: 'Deposit' })).not.toBeInTheDocument()
  })

  it('offers no actions or funding on a closed account', async () => {
    setup(acc({ closedAt: '2026-01-01T00:00:00Z' }))
    renderApp(<Accounts />)
    const dialog = await openCard()
    expect(await within(dialog).findByText('History')).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: /withdraw|deposit/i })).not.toBeInTheDocument()
    expect(within(dialog).queryByText(/funded today|skipped today/i)).not.toBeInTheDocument()
  })

  it('withdraws in cents with an idempotency key, and shows the posted balance as available', async () => {
    const calls = setup(acc(), (m, p) => (m === 'POST' && p === '/accounts/7/transactions' ? { id: 1 } : undefined))
    const fetchSpy = globalThis.fetch as unknown as { mock: { calls: [string, RequestInit][] } }
    renderApp(<Accounts />)
    const dialog = await openCard()
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Withdraw' }))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText('Available: $10.00')).toBeInTheDocument()
    await userEvent.type(within(sheet).getByLabelText('Amount'), '3.5')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true))
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({ path: '/accounts/7/transactions', body: { type: 'withdrawal', amount: 350 } })
    const post = fetchSpy.mock.calls.find(([, i]) => i.method === 'POST')!
    expect((post[1].headers as Record<string, string>)['Idempotency-Key']).toBeTruthy()
  })

  it('sends an optional note with a withdrawal', async () => {
    const calls = setup(acc(), (m, p) => (m === 'POST' && p === '/accounts/7/transactions' ? { id: 1 } : undefined))
    renderApp(<Accounts />)
    const dialog = await openCard()
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Withdraw' }))
    const sheet = await screen.findByRole('dialog')
    await userEvent.type(within(sheet).getByLabelText('Amount'), '1')
    await userEvent.type(within(sheet).getByLabelText('Note (optional)'), 'Candy')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true))
    expect(calls.find((c) => c.method === 'POST')!.body).toEqual({ type: 'withdrawal', amount: 100, comment: 'Candy' })
  })

  it('shows the server message when a withdrawal is refused', async () => {
    setup(acc(), (m, p) => (m === 'POST' && p.endsWith('/transactions') ? err(409, 'insufficient funds') : undefined))
    renderApp(<Accounts />)
    const dialog = await openCard()
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Withdraw' }))
    const sheet = await screen.findByRole('dialog')
    await userEvent.type(within(sheet).getByLabelText('Amount'), '99')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Send' }))
    expect(await within(sheet).findByText('insufficient funds')).toBeInTheDocument()
  })

  it('lets a child request a deposit via POST /requests', async () => {
    const calls = setup(acc({ role: 'user' }), (m, p) => (m === 'POST' && p === '/requests' ? { id: 1 } : undefined))
    renderApp(<Accounts />)
    const dialog = await openCard()
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Request deposit' }))
    const sheet = await screen.findByRole('dialog')
    await userEvent.type(within(sheet).getByLabelText('Amount'), '2.5')
    await userEvent.type(within(sheet).getByLabelText('Note (optional)'), 'Allowance')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.path === '/requests')).toBe(true))
    expect(calls.find((c) => c.path === '/requests' && c.method === 'POST')!.body).toEqual({ accountId: 7, amount: 250, comment: 'Allowance' })
  })
})
