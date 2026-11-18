import { useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ActionIcon, Alert, Button, Card, Collapse, Group, Loader, Modal, NumberInput, Stack, Text, TextInput, ThemeIcon, UnstyledButton } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useTranslation } from 'react-i18next'
import { api, newKey, type Balance, type FundingRequest, type Page, type Transaction } from '../api'
import { formatCents, today, toCents } from '../money'
import { useOffline } from '../offline'

type Kind = 'deposit' | 'request' | 'withdraw'

// Funding is only worth showing on an open account that has a daily amount set.
export const showsFunding = (a: Balance) => (a.dailyFunding ?? 0) > 0 && !a.closedAt

function AmountSheet({ account, kind, onClose }: { account: Balance; kind: Kind; onClose: () => void }) {
  const { t } = useTranslation()
  const { isOffline } = useOffline()
  const qc = useQueryClient()
  const [amount, setAmount] = useState<number | string>('')
  const [comment, setComment] = useState('')
  // One key per open sheet: a double tap or a retry can't post twice.
  const [key] = useState(newKey)

  const send = useMutation({
    mutationFn: () => {
      const cents = toCents(Number(amount))
      if (kind === 'request') {
        return api('POST', '/requests', { key, body: { accountId: account.accountId, amount: cents, comment: comment || undefined } })
      }
      return api('POST', `/accounts/${account.accountId}/transactions`, {
        key,
        body: { type: kind === 'deposit' ? 'deposit' : 'withdrawal', amount: cents, comment: comment || undefined },
      })
    },
    onSuccess: async () => {
      await Promise.all(['accounts', 'transactions', 'requests'].map((k) => qc.invalidateQueries({ queryKey: [k] })))
      onClose()
    },
  })

  const title = kind === 'deposit' ? t('account.deposit') : kind === 'request' ? t('account.requestDeposit') : t('account.withdraw')
  const valid = Number(amount) > 0 && toCents(Number(amount)) > 0

  return (
    <Modal opened onClose={onClose} title={title} centered>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (valid && !isOffline) send.mutate()
        }}
      >
        <Stack>
          {kind === 'withdraw' && <Text size="sm" c="dimmed">{t('account.available', { amount: formatCents(account.balance ?? 0) })}</Text>}
          <NumberInput label={t('account.amount')} prefix="$" decimalScale={2} fixedDecimalScale min={0} hideControls value={amount} onChange={setAmount} data-autofocus />
          <TextInput label={t('account.comment')} maxLength={200} value={comment} onChange={(e) => setComment(e.currentTarget.value)} />
          {send.isError && <Alert color="red">{send.error.message}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>{t('common.cancel')}</Button>
            <Button type="submit" loading={send.isPending} disabled={!valid || isOffline}>{t('common.send')}</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}

function Funding({ account }: { account: Balance }) {
  const { t } = useTranslation()
  const { isOffline } = useOffline()
  const qc = useQueryClient()
  const skipped = !!account.skippedToday
  const toggle = useMutation({
    mutationFn: () => api(skipped ? 'DELETE' : 'PUT', `/accounts/${account.accountId}/funding-skips/${today()}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['accounts'] }),
  })
  const color = skipped ? 'red' : 'green'
  const icon = skipped ? '✗' : '✓'
  const caption = skipped ? t('funding.skippedToday') : t('funding.fundedToday')
  return (
    <Stack gap={4} mt="sm" align="flex-end">
      <Group gap="xs" wrap="nowrap">
        <Text size="sm" c={color}>{caption}</Text>
        {account.role === 'manager' ? (
          <ActionIcon
            size={44} radius="xl" variant="light" color={color} fz={22}
            loading={toggle.isPending}
            disabled={isOffline}
            aria-label={skipped ? t('funding.turnOn') : t('funding.turnOff')}
            title={isOffline ? t('common.unavailableOffline') : skipped ? t('funding.turnOn') : t('funding.turnOff')}
            onClick={() => !isOffline && toggle.mutate()}
          >
            {icon}
          </ActionIcon>
        ) : (
          <ThemeIcon size={44} radius="xl" variant="light" color={color} fz={22} role="img" aria-label={caption}>{icon}</ThemeIcon>
        )}
      </Group>
      {toggle.isError && <Alert color="red">{toggle.error.message}</Alert>}
    </Stack>
  )
}

function History({ accountId }: { accountId: number }) {
  const { t } = useTranslation()
  const q = useInfiniteQuery({
    queryKey: ['transactions', accountId],
    initialPageParam: 1,
    queryFn: ({ pageParam }) => api<Page<Transaction>>('GET', `/accounts/${accountId}/transactions?page=${pageParam}&pageSize=20`),
    getNextPageParam: (last) => (last.page * last.pageSize < last.total ? last.page + 1 : undefined),
  })
  const rows = q.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <Stack gap="xs">
      <Text fw={600}>{t('account.history')}</Text>
      {q.isPending && <Loader size="sm" />}
      {q.isSuccess && rows.length === 0 && <Text c="dimmed" size="sm">{t('account.noHistory')}</Text>}
      {rows.map((r) => (
        <Stack key={r.id} gap={0}>
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm">
              {r.personId === null ? t('account.autoFunding') : r.type === 'deposit' ? t('account.deposit') : t('account.withdraw')}
              <Text span c="dimmed"> · {new Date(r.createdAt!).toLocaleDateString()}</Text>
            </Text>
            <Text size="sm" c={r.type === 'deposit' ? 'green' : 'red'}>
              {r.type === 'deposit' ? '+' : '−'}{formatCents(r.amount ?? 0)}
            </Text>
          </Group>
          {r.comment && <Text size="xs" c="dimmed">{r.comment}</Text>}
        </Stack>
      ))}
      {q.hasNextPage && <Button variant="subtle" onClick={() => q.fetchNextPage()} loading={q.isFetchingNextPage}>{t('common.loadMore')}</Button>}
    </Stack>
  )
}

function PendingList({ accountId }: { accountId: number }) {
  const { t } = useTranslation()
  const q = useQuery({
    queryKey: ['requests', 'account', accountId],
    queryFn: () => api<Page<FundingRequest>>('GET', `/requests?accountId=${accountId}&status=pending&pageSize=100`),
  })
  if (!q.data?.items.length) return null
  return (
    <Stack gap="xs">
      <Text fw={600}>{t('account.pendingTitle')}</Text>
      {q.data.items.map((r) => (
        <Group key={r.id} justify="space-between">
          <Text size="sm">⏳ {r.requesterComment ?? ''}</Text>
          <Text size="sm" c="orange">+{formatCents(r.amount ?? 0)}</Text>
        </Group>
      ))}
    </Stack>
  )
}

// Mounted only while the card is open, so history and pending load on demand.
function Details({ account }: { account: Balance }) {
  const { t } = useTranslation()
  const { isOffline } = useOffline()
  const [kind, setKind] = useState<Kind | null>(null)
  const manager = account.role === 'manager'
  return (
    <Stack mt="md">
      {!account.closedAt && (
        <Group grow>
          <Button disabled={isOffline} onClick={() => setKind(manager ? 'deposit' : 'request')}>{manager ? t('account.deposit') : t('account.requestDeposit')}</Button>
          <Button disabled={isOffline} variant="default" onClick={() => setKind('withdraw')}>{t('account.withdraw')}</Button>
        </Group>
      )}
      <PendingList accountId={account.accountId!} />
      <History accountId={account.accountId!} />
      {kind && <AmountSheet account={account} kind={kind} onClose={() => setKind(null)} />}
    </Stack>
  )
}

export function AccountCard({ account: a }: { account: Balance }) {
  const { t } = useTranslation()
  const { isOffline } = useOffline()
  const [opened, { toggle }] = useDisclosure(false)
  const canExpand = !isOffline

  return (
    <Card withBorder radius="md" role="group" aria-label={a.name}>
      <UnstyledButton
        onClick={canExpand ? toggle : undefined}
        aria-expanded={canExpand ? opened : undefined}
        aria-disabled={!canExpand}
        aria-label={a.name}
        w="100%"
        style={!canExpand ? { cursor: 'default' } : undefined}
      >
        <Group justify="space-between" wrap="nowrap">
          <Text fw={600}>{a.name} {a.closedAt && <Text span c="dimmed">({t('accounts.closed')})</Text>}</Text>
          {canExpand && <Text c="dimmed" aria-hidden>{opened ? '▴' : '▾'}</Text>}
        </Group>
      </UnstyledButton>
      <Text size="xl" fw={700}>{formatCents(a.balance ?? 0)}</Text>
      {(a.pending ?? 0) > 0 && <Text size="sm" c="orange">{t('accounts.pending', { amount: formatCents(a.pending ?? 0) })}</Text>}
      {showsFunding(a) && <Funding account={a} />}
      <Collapse expanded={canExpand && opened}>{canExpand && opened && <Details account={a} />}</Collapse>
    </Card>
  )
}
