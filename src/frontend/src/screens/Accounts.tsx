import { useQuery } from '@tanstack/react-query'
import { Alert, Box, Group, Loader, Stack, Text, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { api, loadCachedAccounts, saveCachedAccounts, type Balance } from '../api'
import { useAuth } from '../auth'
import { formatCents } from '../money'
import { useOffline } from '../offline'
import { AccountCard } from './AccountCard'

export const accountsKey = ['accounts']

export function Accounts() {
  const { t } = useTranslation()
  const { session } = useAuth()
  const { isOffline } = useOffline()

  const cached = loadCachedAccounts()

  const q = useQuery({
    queryKey: accountsKey,
    queryFn: async () => {
      const data = await api<Balance[]>('GET', '/accounts')
      saveCachedAccounts(data)
      return data
    },
    initialData: cached ?? undefined,
    enabled: !isOffline || !cached,
  })

  const accounts = q.data ?? []
  const totalBalance = accounts.reduce((acc, a) => acc + (a.balance ?? 0), 0)

  return (
    <Stack>
      <Group justify="space-between" align="baseline">
        <Title order={2}>{session?.familyName ?? t('accounts.title')}</Title>
        {accounts.length > 0 && (
          <Group gap="xs" align="baseline">
            <Text size="sm" c="dimmed">{t('accounts.totalBalance')}:</Text>
            <Text size="lg" fw={700}>{formatCents(totalBalance)}</Text>
          </Group>
        )}
      </Group>

      {q.isPending && !cached && <Loader />}
      {!isOffline && q.isError && <Alert color="red">{q.error.message}</Alert>}
      {isOffline && accounts.length === 0 && (
        <Alert color="yellow">{t('accounts.offlineNoData')}</Alert>
      )}
      {!q.isPending && !q.isError && accounts.length === 0 && !isOffline && (
        <Text c="dimmed">{t('accounts.empty')}</Text>
      )}

      <Box
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 320px), 440px))',
          justifyContent: 'center',
          alignItems: 'start',
          gap: 'var(--mantine-spacing-md)',
        }}
      >
        {accounts
          .slice()
          .sort((a, b) => (a.name ?? '').localeCompare(b.name ?? ''))
          .map((a) => (
            <AccountCard key={a.accountId} account={a} />
          ))}
      </Box>
    </Stack>
  )
}
