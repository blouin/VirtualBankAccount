import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Badge, Button, Card, Collapse, Group, PasswordInput, SegmentedControl, Stack, Text, Title, UnstyledButton, useMantineColorScheme } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useTranslation } from 'react-i18next'
import { api, type DeviceSession } from '../api'
import { useAuth } from '../auth'
import { setLang } from '../i18n'
import { useOffline } from '../offline'

function ChangePassword() {
  const { t } = useTranslation()
  const { isOffline } = useOffline()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const save = useMutation({
    mutationFn: () => api('PUT', '/auth/changePassword', { body: { currentPassword: current, newPassword: next } }),
    onSuccess: () => {
      setCurrent('')
      setNext('')
    },
  })
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (!isOffline) save.mutate()
      }}
    >
      <Stack>
        <Text fw={600}>{t('settings.changePassword')}</Text>
        <PasswordInput disabled={isOffline} label={t('settings.current')} value={current} onChange={(e) => setCurrent(e.currentTarget.value)} />
        <PasswordInput disabled={isOffline} label={t('settings.next')} value={next} onChange={(e) => setNext(e.currentTarget.value)} />
        {save.isError && <Alert color="red">{save.error.message}</Alert>}
        {save.isSuccess && <Alert color="green">{t('settings.passwordChanged')}</Alert>}
        <Button type="submit" loading={save.isPending} disabled={isOffline || current === '' || next.length < 6} w="fit-content">{t('settings.save')}</Button>
      </Stack>
    </form>
  )
}

function Devices() {
  const { t } = useTranslation()
  const { isOffline } = useOffline()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['sessions'], queryFn: () => api<DeviceSession[]>('GET', '/auth/sessions'), enabled: !isOffline })
  const revoke = useMutation({
    mutationFn: (id: number) => api('DELETE', `/auth/sessions/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sessions'] }),
  })
  return (
    <Stack>
      <Text fw={600}>{t('settings.devices')}</Text>
      {q.data?.map((s) => (
        <Card key={s.id} withBorder radius="md" p="sm">
          <Group justify="space-between" wrap="nowrap">
            <div>
              <Text size="sm">
                {s.device || t('settings.unknownDevice')}
                {s.current && <Text span c="blue"> ({t('settings.thisDevice')})</Text>}
              </Text>
              <Text size="xs" c="dimmed">
                {s.expiresAt ? t('settings.expires', { date: new Date(s.expiresAt).toLocaleDateString() }) : t('settings.neverExpires')}
              </Text>
            </div>
            {!s.current && <Button size="xs" variant="light" color="red" disabled={isOffline} onClick={() => revoke.mutate(s.id)}>{t('settings.revoke')}</Button>}
          </Group>
        </Card>
      ))}
      {revoke.isError && <Alert color="red">{revoke.error.message}</Alert>}
    </Stack>
  )
}

function DangerZone() {
  const { t } = useTranslation()
  const { isOffline } = useOffline()
  const [opened, { toggle }] = useDisclosure(false)
  const canOpen = !isOffline

  return (
    <Card withBorder radius="md" style={{ borderColor: 'var(--mantine-color-red-7)', opacity: isOffline ? 0.6 : 1 }}>
      <UnstyledButton
        onClick={canOpen ? toggle : undefined}
        aria-expanded={canOpen ? opened : undefined}
        aria-disabled={!canOpen}
        w="100%"
        style={!canOpen ? { cursor: 'default' } : undefined}
      >
        <Group justify="space-between" wrap="nowrap">
          <div>
            <Group gap="xs">
              <Text fw={600} c="red">{t('settings.dangerZone')}</Text>
              {isOffline && <Badge size="xs" color="gray">{t('common.offline')}</Badge>}
            </Group>
            <Text size="sm" c="dimmed">
              {isOffline ? t('settings.dangerZoneOffline') : t('settings.dangerHint')}
            </Text>
          </div>
          {canOpen && <Text c="dimmed" aria-hidden>{opened ? '▴' : '▾'}</Text>}
        </Group>
      </UnstyledButton>
      <Collapse expanded={canOpen && opened}>
        {canOpen && opened && (
          <Stack mt="md" gap="xl">
            <ChangePassword />
            <Devices />
          </Stack>
        )}
      </Collapse>
    </Card>
  )
}

export function Settings() {
  const { t, i18n } = useTranslation()
  const { session, end } = useAuth()
  // Mantine keeps the choice in localStorage ("mantine-color-scheme-value").
  const { colorScheme: scheme, setColorScheme } = useMantineColorScheme()
  return (
    <Stack gap="lg">
      <Title order={2}>{t('settings.title')}</Title>
      <Stack gap="xs">
        <Text fw={600}>{t('settings.connected')}</Text>
        <SegmentedControl readOnly fullWidth styles={{ label: { textAlign: 'left', paddingLeft: 'var(--mantine-spacing-md)' } }} value={session?.person.name ?? ''} data={[session?.person.name ?? '']} />
      </Stack>
      <Stack gap="xs">
        <Text fw={600}>{t('settings.language')}</Text>
        <SegmentedControl
          value={i18n.language.startsWith('fr') ? 'fr' : 'en'}
          onChange={(v) => setLang(v as 'en' | 'fr')}
          data={[{ value: 'en', label: 'English' }, { value: 'fr', label: 'Français' }]}
        />
      </Stack>
      <Stack gap="xs">
        <Text fw={600}>{t('settings.appearance')}</Text>
        <SegmentedControl
          value={scheme === 'auto' ? 'dark' : scheme}
          onChange={(v) => setColorScheme(v as 'light' | 'dark')}
          data={[
            { value: 'light', label: t('settings.light') },
            { value: 'dark', label: t('settings.dark') },
          ]}
        />
      </Stack>
      <DangerZone />
      <Button color="red" variant="light" onClick={() => end()}>{t('settings.logOff')}</Button>
    </Stack>
  )
}
