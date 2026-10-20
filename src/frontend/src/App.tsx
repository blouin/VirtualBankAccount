import { useQuery } from '@tanstack/react-query'
import { AppShell, Badge, Group, Stack, Text, UnstyledButton } from '@mantine/core'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, type Balance, type FundingRequest, type Page } from './api'
import { useAuth } from './auth'
import { Accounts, accountsKey } from './screens/Accounts'
import { Login } from './screens/Login'
import { Requests } from './screens/Requests'
import { Settings } from './screens/Settings'

function Tab({ to, label, badge }: { to: string; label: string; badge?: number }) {
  return (
    <UnstyledButton component={NavLink} to={to} style={{ flex: 1, textAlign: 'center', padding: 12 }}>
      {({ isActive }: { isActive: boolean }) => (
        <Group justify="center" gap={6}>
          <Text fw={isActive ? 700 : 400} c={isActive ? 'blue' : undefined}>{label}</Text>
          {!!badge && <Badge size="sm" circle>{badge}</Badge>}
        </Group>
      )}
    </UnstyledButton>
  )
}

function Shell() {
  const { t } = useTranslation()
  const accounts = useQuery({ queryKey: accountsKey, queryFn: () => api<Balance[]>('GET', '/accounts') })
  const isManager = !!accounts.data?.some((a) => a.role === 'manager')
  // Counts every pending request on the caller's accounts (a manager's own requests included); refreshed by the screens' invalidations.
  const pending = useQuery({
    queryKey: ['requests', 'badge'],
    queryFn: () => api<Page<FundingRequest>>('GET', '/requests?status=pending&pageSize=1'),
    enabled: isManager,
  })

  return (
    <AppShell footer={{ height: 56 }} padding="md">
      <AppShell.Main>
        <Stack maw={1200} mx="auto">
          <Routes>
            <Route path="/" element={<Accounts />} />
            <Route path="/requests" element={<Stack maw={640} w="100%" mx="auto"><Requests /></Stack>} />
            <Route path="/settings" element={<Stack maw={640} w="100%" mx="auto"><Settings /></Stack>} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Stack>
      </AppShell.Main>
      <AppShell.Footer>
        <Group gap={0} h="100%" maw={640} mx="auto" style={{ paddingBottom: 'env(safe-area-inset-bottom)' }}>
          <Tab to="/" label={t('nav.accounts')} />
          <Tab to="/requests" label={t('nav.requests')} badge={pending.data?.total} />
          <Tab to="/settings" label={t('nav.settings')} />
        </Group>
      </AppShell.Footer>
    </AppShell>
  )
}

export default function App() {
  const { session } = useAuth()
  return session ? <Shell /> : <Login />
}
