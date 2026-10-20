import { useQuery } from '@tanstack/react-query'
import { Alert, Box, Loader, Stack, Text, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { api, type Balance } from '../api'
import { AccountCard } from './AccountCard'

export const accountsKey = ['accounts']

export function Accounts() {
  const { t } = useTranslation()
  const q = useQuery({ queryKey: accountsKey, queryFn: () => api<Balance[]>('GET', '/accounts') })

  return (
    <Stack>
      <Title order={2}>{t('accounts.title')}</Title>
      {q.isPending && <Loader />}
      {q.isError && <Alert color="red">{q.error.message}</Alert>}
      {q.data?.length === 0 && <Text c="dimmed">{t('accounts.empty')}</Text>}
      <Box
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 320px), 440px))',
          justifyContent: 'center',
          alignItems: 'start',
          gap: 'var(--mantine-spacing-md)',
        }}
      >
        {q.data?.slice().sort((a, b) => (a.name ?? '').localeCompare(b.name ?? '')).map((a) => <AccountCard key={a.accountId} account={a} />)}
      </Box>
    </Stack>
  )
}
