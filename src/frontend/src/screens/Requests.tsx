import { useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Badge, Button, Card, Group, Loader, Menu, Modal, SegmentedControl, Stack, Text, TextInput, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { api, type Balance, type FundingRequest, type Page, type Person } from '../api'
import { useAuth } from '../auth'
import { formatCents } from '../money'
import { accountsKey } from './Accounts'

export function Requests() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { session } = useAuth()
  const [filter, setFilter] = useState<'pending' | 'all'>('pending')
  const [dialog, setDialog] = useState<{ id: number; action: 'approve' | 'reject' } | null>(null)
  const [note, setNote] = useState('')

  const accounts = useQuery({ queryKey: accountsKey, queryFn: () => api<Balance[]>('GET', '/accounts') })
  // Names for "requested by"; the public login list is enough, and a miss falls back to the id.
  const persons = useQuery({
    queryKey: ['persons', session?.familyCode],
    queryFn: () => api<Person[]>('GET', `/auth/persons?familyCode=${encodeURIComponent(session!.familyCode)}`, { auth: false }),
    enabled: !!session,
  })
  const list = useInfiniteQuery({
    queryKey: ['requests', 'list', filter],
    initialPageParam: 1,
    queryFn: ({ pageParam }) =>
      api<Page<FundingRequest>>('GET', `/requests?${filter === 'pending' ? 'status=pending&' : ''}page=${pageParam}&pageSize=20`),
    getNextPageParam: (last) => (last.page * last.pageSize < last.total ? last.page + 1 : undefined),
  })

  const decide = useMutation({
    mutationFn: (v: { id: number; action: 'approve' | 'reject'; comment?: string }) =>
      api('POST', `/requests/${v.id}/${v.action}`, { body: v.comment ? { comment: v.comment } : undefined }),
    onSettled: async () => {
      setDialog(null)
      setNote('')
      await Promise.all(['requests', 'accounts', 'transactions'].map((k) => qc.invalidateQueries({ queryKey: [k] })))
    },
  })

  const accountOf = (id?: number) => accounts.data?.find((a) => a.accountId === id)
  const nameOf = (id?: number) => persons.data?.find((p) => p.id === id)?.name ?? `#${id}`
  const rows = (list.data?.pages.flatMap((p) => p.items) ?? []).sort((a, b) => (b.requestedAt ?? '').localeCompare(a.requestedAt ?? ''))

  return (
    <Stack>
      <Title order={2}>{t('requests.title')}</Title>
      <SegmentedControl
        value={filter}
        onChange={(v) => setFilter(v as 'pending' | 'all')}
        data={[{ value: 'pending', label: t('requests.pending') }, { value: 'all', label: t('requests.all') }]}
      />
      {list.isPending && <Loader />}
      {list.isError && <Alert color="red">{list.error.message}</Alert>}
      {decide.isError && <Alert color="red">{decide.error.message}</Alert>}
      {list.isSuccess && rows.length === 0 && <Text c="dimmed">{t('requests.none')}</Text>}
      {rows.map((r) => {
        const acc = accountOf(r.accountId)
        return (
          <Card key={r.id} withBorder radius="md">
            <Group justify="space-between">
              <Text fw={600}>{acc?.name ?? `#${r.accountId}`} · +{formatCents(r.amount ?? 0)}</Text>
              {r.status !== 'pending' && <Badge color={r.status === 'approved' ? 'green' : 'red'}>{t(`requests.status.${r.status}`)}</Badge>}
            </Group>
            <Text size="sm" c="dimmed">{t('requests.requestedBy', { name: nameOf(r.requestedBy), date: new Date(r.requestedAt!).toLocaleString() })}</Text>
            {r.requesterComment && <Text size="sm">{t('requests.requesterComment', { comment: r.requesterComment })}</Text>}
            {r.status !== 'pending' && r.deciderComment && (
              <Text size="sm" c={r.status === 'approved' ? 'green' : 'red'}>
                {t('requests.approverComment', { comment: r.deciderComment })}
              </Text>
            )}
            {r.status === 'pending' && acc?.role === 'manager' && (
              <Group mt="sm" justify="flex-end">
                <Button variant="default" disabled={decide.isPending} onClick={() => setDialog({ id: r.id!, action: 'reject' })}>{t('requests.reject')}</Button>
                <Button.Group>
                  <Button disabled={decide.isPending} onClick={() => decide.mutate({ id: r.id!, action: 'approve' })}>{t('requests.approve')}</Button>
                  <Menu position="bottom-end" withinPortal>
                    <Menu.Target>
                      <Button px="xs" disabled={decide.isPending} aria-label={t('requests.moreApprove')}>▾</Button>
                    </Menu.Target>
                    <Menu.Dropdown>
                      <Menu.Item onClick={() => setDialog({ id: r.id!, action: 'approve' })}>{t('requests.approveWithComment')}</Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                </Button.Group>
              </Group>
            )}
          </Card>
        )
      })}
      {list.hasNextPage && <Button variant="subtle" onClick={() => list.fetchNextPage()} loading={list.isFetchingNextPage}>{t('common.loadMore')}</Button>}
      <Modal opened={dialog !== null} onClose={() => setDialog(null)} title={dialog?.action === 'approve' ? t('requests.approveTitle') : t('requests.rejectTitle')} centered>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            decide.mutate({ id: dialog!.id, action: dialog!.action, comment: note.trim() })
          }}
        >
          <Stack>
            <TextInput label={t('requests.reason')} maxLength={200} value={note} onChange={(e) => setNote(e.currentTarget.value)} data-autofocus />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setDialog(null)}>{t('common.cancel')}</Button>
              <Button type="submit" color={dialog?.action === 'approve' ? undefined : 'red'} loading={decide.isPending}>
                {dialog?.action === 'approve' ? t('requests.approve') : t('requests.reject')}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  )
}
