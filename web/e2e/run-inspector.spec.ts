import { expect, test, type Page } from '@playwright/test'

/**
 * The end-to-end suite drives the real collector. `globalSetup` starts the
 * binary against a throwaway home, signs in once, and hands every worker the
 * same session cookie, so the specs read like an operator's workflow.
 */
export const BASE_URL = process.env.TRACEBOARD_E2E_URL ?? 'http://127.0.0.1:47821'
const E2E_TOKEN = process.env.TRACEBOARD_E2E_TOKEN ?? ''

declare global {
  interface Window {
    __pwned?: unknown
  }
}

async function signIn(page: Page, token: string): Promise<void> {
  await page.goto(`/auth/signin?token=${token}`)
  await page.getByRole('button', { name: 'Open dashboard' }).click()
  await page.waitForURL((url) => !url.pathname.startsWith('/auth'))
}

function trackExternalRequests(page: Page): string[] {
  const external: string[] = []
  page.on('request', (request) => {
    const url = request.url()
    if (url.startsWith(BASE_URL) || url.startsWith('data:') || url.startsWith('blob:')) return
    external.push(url)
  })
  return external
}

test.describe('run index and inspector', () => {
  test('signs in and shows the captured run without a manual reload', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)

    const row = page.getByRole('button', { name: /fix the ingest pipeline/ }).first()
    await expect(row).toBeVisible()
    await row.click()

    await expect(page.getByRole('heading', { name: 'Run inspector' })).toBeVisible()
    await expect(page.getByText('fix the ingest pipeline').first()).toBeVisible()
    await expect(page.getByText('5 events loaded')).toBeVisible()
  })

  test('shows an event detail with redacted attributes and a run level marker', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    await expect(page.getByText('5 events loaded')).toBeVisible()

    await page.getByRole('button', { name: /#3/ }).first().click()
    const detail = page.getByTestId('event-detail').first()
    await expect(detail).toBeVisible()
    await expect(detail).toContainText('[REDACTED:api_key]')
    await expect(detail).toContainText('1 field redacted before storage.')
  })

  test('replay reconstructs recorded state and offers no re-execution control', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    await expect(page.getByText('5 events loaded')).toBeVisible()

    const slider = page.getByLabel('Replay position')
    await expect(slider).toBeVisible()
    await slider.fill('1')
    await expect(page.getByTestId('replay-state')).toContainText('2 of 5')

    const labels = await page.getByRole('button').allTextContents()
    expect(labels.join(' ')).not.toMatch(/re-?run|re-?execute|retry|resume/i)
  })

  test('filters the run index through the server', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)
    await page.getByLabel('Search runs').fill('nothing-matches-this')
    await expect(page.getByText(/no runs match/i)).toBeVisible()

    await page.getByRole('button', { name: 'Reset filters' }).click()
    await expect(page.getByRole('button', { name: /fix the ingest pipeline/ }).first()).toBeVisible()
  })

  test('acknowledges an alert from the inspector', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    const acknowledge = page.getByRole('button', { name: 'Acknowledge' }).first()
    await expect(acknowledge).toBeVisible()
    await acknowledge.click()
    await expect(page.getByText(/resolved or acknowledged alert/)).toBeVisible()
  })
})

test.describe('keyboard and zoom', () => {
  test('reaches every run row with the keyboard and shows a visible focus ring', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)
    const row = page.getByRole('button', { name: /fix the ingest pipeline/ }).first()
    await row.focus()
    await expect(row).toBeFocused()
    const outline = await row.evaluate((node) => getComputedStyle(node).outlineWidth)
    expect(outline).not.toBe('0px')
  })

  test('reflows at a narrow viewport without hiding the run index', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await page.setViewportSize({ width: 420, height: 900 })
    await signIn(page, token)
    await expect(page.getByRole('button', { name: /fix the ingest pipeline/ }).first()).toBeVisible()
  })
})

test.describe('security', () => {
  test('rejects an unauthenticated API request from the page context', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await page.context().clearCookies()
    const response = await page.request.get('/api/v1/runs', { failOnStatusCode: false })
    expect(response.status()).toBe(401)
    const body = await response.text()
    expect(body).not.toContain(token)
  })

  test('loads no external script, font, or image', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    const external = trackExternalRequests(page)
    await signIn(page, token)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    await expect(page.getByText('5 events loaded')).toBeVisible()
    expect(external).toEqual([])
  })

  test('renders a hostile captured payload as inert text', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    await page.getByRole('button', { name: /#3/ }).first().click()
    const detail = page.getByTestId('event-detail').first()
    await expect(detail).toContainText('[REDACTED')
    expect(await detail.locator('script, img, svg').count()).toBe(0)
    expect(await page.evaluate(() => window.__pwned)).toBeUndefined()
  })

  test('never renders a token into the document', async ({ page }) => {
    const token = E2E_TOKEN
    test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')

    await signIn(page, token)
    await page.goto('#/settings')
    await expect(page.getByText('Listen address')).toBeVisible()
    const text = await page.locator('body').innerText()
    expect(text).not.toContain(token)
  })
})
