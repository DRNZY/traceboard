import { spawn, type ChildProcess } from 'node:child_process'
import { createServer } from 'node:net'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'

/**
 * The browser suite drives the real embedded binary over loopback. Nothing is
 * stubbed: the collector under test is `bin/traceboard` started against a
 * throwaway home on its own ephemeral port, so the suite exercises the same
 * assets and routes an operator uses without disturbing a real instance.
 */
let server: ChildProcess | undefined
let workdir = ''

type Config = { ingest_token: string; listen_address: string }

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const probe = createServer()
    probe.on('error', reject)
    probe.listen(0, '127.0.0.1', () => {
      const address = probe.address()
      if (address === null || typeof address === 'string') {
        probe.close(() => reject(new Error('could not reserve a loopback port')))
        return
      }
      const { port } = address
      probe.close(() => resolve(port))
    })
  })
}

async function waitForHealth(baseURL: string): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    try {
      const response = await fetch(`${baseURL}/health`)
      if (response.ok) return
    } catch {
      // The listener is not up yet.
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`the collector at ${baseURL} did not become healthy`)
}

async function startCollector(root: string, configPath: string, address: string): Promise<ChildProcess> {
  const child = spawn(join(root, 'bin', 'traceboard'), ['start', '--no-browser'], {
    env: { ...process.env, TRACEBOARD_CONFIG: configPath, TRACEBOARD_LISTEN: address },
    stdio: 'ignore',
    detached: true,
  })
  child.unref()
  return child
}

async function readSignInToken(configPath: string): Promise<string> {
  const raw = await readFile(join(dirname(configPath), 'signin-url'), 'utf8')
  const url = raw.match(/"url":\s*"([^"]+)"/)?.[1]
  if (!url) throw new Error('the sign-in url file did not contain a url')
  const token = new URL(url).searchParams.get('token')
  if (!token) throw new Error('the sign-in url did not contain a token')
  return token
}

function nowUTC(sequence: number): string {
  return new Date(Date.UTC(2026, 8, 25, 10, 0, sequence)).toISOString()
}

async function seed(baseURL: string, token: string): Promise<void> {
  const event = (index: number, overrides: Record<string, unknown>) => ({
    schema_version: 1,
    event_id: `e2e-${index}`,
    source_event_id: `e2e-src-${index}`,
    source: 'opencode',
    source_version: '1.0.0',
    run_id: 'e2e_run_1',
    occurred_at: nowUTC(0),
    type: 'tool.started',
    status: 'started',
    capture: { mode: 'detailed' },
    attributes: {},
    ...overrides,
  })
  const response = await fetch(`${baseURL}/api/v1/events`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({
      events: [
        event(1, { type: 'run.started', status: 'started' }),
        event(2, {
          type: 'prompt.received',
          status: 'completed',
          occurred_at: nowUTC(1),
          step_id: 'step_1',
          content: { text: 'fix the ingest pipeline' },
        }),
        event(3, { type: 'metadata.source', status: 'unknown', occurred_at: nowUTC(2), step_id: 'step_1' }),
        event(4, {
          type: 'tool.failed',
          status: 'failed',
          occurred_at: nowUTC(2),
          step_id: 'step_1',
          attributes: { tool: 'bash', api_key: 'sk-abcdefghijklmnopqrstuvwx' },
        }),
        event(5, { type: 'run.failed', status: 'failed', occurred_at: nowUTC(3) }),
      ],
    }),
  })
  if (!response.ok) {
    throw new Error(`ingest failed with ${response.status}: ${await response.text()}`)
  }
  // A source-reported failure opens an alert on the next evaluation pass.
  await new Promise((resolve) => setTimeout(resolve, 2000))
}

export default async function globalSetup(): Promise<void> {
  const root = process.env.TRACEBOARD_ROOT ?? join(process.cwd(), '..')
  workdir = await mkdtemp(join(tmpdir(), 'traceboard-e2e-'))
  const configPath = join(workdir, 'config.json')
  const port = await freePort()
  const address = `127.0.0.1:${port}`
  const baseURL = `http://${address}`

  process.env.TRACEBOARD_E2E_URL = baseURL

  // First start creates the protected config; detailed capture is then recorded
  // so the inspector has content to show through the normal code path.
  server = await startCollector(root, configPath, address)
  await waitForHealth(baseURL)

  const config = JSON.parse(await readFile(configPath, 'utf8')) as Config
  process.env.TRACEBOARD_E2E_TOKEN = await readSignInToken(configPath)

  const updated = { ...config, sources: { opencode: { capture_mode: 'detailed' } } } as Config
  await writeFile(configPath, JSON.stringify(updated, null, 2), { mode: 0o600 })

  server.kill('SIGTERM')
  await new Promise((resolve) => setTimeout(resolve, 1000))
  server = await startCollector(root, configPath, address)
  await waitForHealth(baseURL)

  await seed(baseURL, config.ingest_token)

  // The printed token is one-time, so the suite signs in exactly once and shares
  // the resulting session cookie with every test.
  process.env.TRACEBOARD_E2E_TOKEN = await readSignInToken(configPath)
  await writeStorageState(baseURL, process.env.TRACEBOARD_E2E_TOKEN, authStatePath())
}

const stateFile = join(process.cwd(), 'e2e', '.auth', 'state.json')

function authStatePath(): string {
  return stateFile
}

/** Exchanges the one-time token and stores the session cookie for every spec. */
async function writeStorageState(baseURL: string, token: string, path: string): Promise<void> {
  const response = await fetch(`${baseURL}/auth/session`, {
    method: 'POST',
    redirect: 'manual',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ token }).toString(),
  })
  const cookies = response.headers.getSetCookie()
  const session = cookies.map((value) => value.split(';')[0]).find((value) => value.startsWith('traceboard_session='))
  if (!session) {
    throw new Error(`the sign-in exchange returned no session cookie (status ${response.status})`)
  }
  const [name, value] = session.split('=')
  const { port } = new URL(baseURL)
  const payload = {
    cookies: [
      {
        name,
        value,
        domain: '127.0.0.1',
        path: '/',
        expires: -1,
        httpOnly: true,
        secure: false,
        sameSite: 'Strict',
      },
    ],
    origins: [],
  }
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, JSON.stringify(payload, null, 2), { mode: 0o600 })
  void port
}

export async function globalTeardown(): Promise<void> {
  server?.kill('SIGTERM')
  if (workdir) {
    await new Promise((resolve) => setTimeout(resolve, 500))
    await rm(workdir, { recursive: true, force: true })
  }
}
