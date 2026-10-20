import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Anchor, Button, Checkbox, Container, Group, PasswordInput, Stack, TextInput, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { ApiError, api, type Person, type Session } from '../api'
import { useAuth } from '../auth'

const FAMILY_KEY = 'vbank.family'

function deviceLabel() {
  const ua = navigator.userAgent
  const browser = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
  const os = /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Windows/.test(ua) ? 'Windows' : /Mac/.test(ua) ? 'Mac' : ''
  return `${browser} ${os}`.trim()
}

export function Login() {
  const { t } = useTranslation()
  const { start } = useAuth()
  const [typed, setTyped] = useState(localStorage.getItem(FAMILY_KEY) ?? '')
  const [family, setFamily] = useState(localStorage.getItem(FAMILY_KEY) ?? '')
  const [personId, setPersonId] = useState<number | null>(null)
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)

  const persons = useQuery({
    queryKey: ['persons', family],
    queryFn: () => api<Person[]>('GET', `/auth/persons?familyCode=${encodeURIComponent(family)}`, { auth: false }),
    enabled: family !== '',
  })

  const login = useMutation({
    mutationFn: () =>
      api<{ token: string; person: Person }>('POST', '/auth/login', {
        auth: false,
        body: { familyCode: family, personId, password, remember, device: deviceLabel() },
      }),
    onSuccess: (r) => {
      localStorage.setItem(FAMILY_KEY, family)
      start({ token: r.token, person: r.person, familyCode: family } satisfies Session)
    },
  })

  const err = login.error instanceof ApiError
    ? login.error.status === 429
      ? t('login.tooMany', { minutes: Math.ceil((login.error.retryAfter ?? 60) / 60) })
      : login.error.message
    : null

  return (
    <Container size="xs" py="xl">
      <Stack>
        <Title order={2}>{t('appName')}</Title>
        {family === '' ? (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              setFamily(typed.trim())
            }}
          >
            <Stack>
              <TextInput label={t('login.familyCode')} value={typed} onChange={(e) => setTyped(e.currentTarget.value)} autoCapitalize="characters" data-autofocus />
              <Button type="submit" disabled={typed.trim() === ''}>{t('login.go')}</Button>
            </Stack>
          </form>
        ) : (
          <>
            <Group justify="space-between">
              <b>{family}</b>
              <Anchor component="button" type="button" onClick={() => { setFamily(''); setPersonId(null); login.reset() }}>{t('login.change')}</Anchor>
            </Group>
            {persons.data?.length === 0 && <Alert color="yellow">{t('login.noPersons')}</Alert>}
            {persons.data && persons.data.length > 0 && (
              <form
                onSubmit={(e) => {
                  e.preventDefault()
                  login.mutate()
                }}
              >
                <Stack>
                  <b>{t('login.who')}</b>
                  <Group>
                    {persons.data.map((p) => (
                      <Button key={p.id} variant={p.id === personId ? 'filled' : 'light'} onClick={() => setPersonId(p.id)}>
                        {p.name}
                      </Button>
                    ))}
                  </Group>
                  {personId !== null && (
                    <>
                      <PasswordInput label={t('login.password')} value={password} onChange={(e) => setPassword(e.currentTarget.value)} data-autofocus />
                      <Checkbox label={t('login.remember')} checked={remember} onChange={(e) => setRemember(e.currentTarget.checked)} />
                      {err && <Alert color="red">{err}</Alert>}
                      <Button type="submit" loading={login.isPending} disabled={password === ''}>{t('login.logIn')}</Button>
                    </>
                  )}
                </Stack>
              </form>
            )}
          </>
        )}
      </Stack>
    </Container>
  )
}
