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

/**
 * Opens the dashboard. The session cookie comes from the shared storage state
 * that `globalSetup` produced, so the clean URL is what gets exercised here.
 */
async function openDashboard(page: Page): Promise<void> {
  await page.goto('/')
  await expect(page.getByRole('link', { name: 'Runs' })).toBeVisible()
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
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)

    const row = page.getByRole('button', { name: /fix the ingest pipeline/ }).first()
    await expect(row).toBeVisible()
    await row.click()

    await expect(page.getByText('Run inspector')).toBeVisible()
    await expect(page.getByText('fix the ingest pipeline').first()).toBeVisible()
    await expect(page.getByText('5 events loaded')).toBeVisible()
  })

  test('shows an event detail with redacted attributes and a run level marker', async ({ page }) => {
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    await expect(page.getByText('5 events loaded')).toBeVisible()

    // Sequence 4 is the failed tool call that carried the seeded secret.
    await page.getByRole('button', { name: /#4/ }).first().click()
    const detail = page.getByTestId('event-detail').first()
    await expect(detail).toBeVisible()
    await expect(detail).toContainText('[REDACTED:api_key]')
    await expect(detail).toContainText('1 field redacted before storage.')

    // The run-level event reports that it carries no step rather than a blank.
    await page.getByRole('button', { name: /#5/ }).first().click()
    await expect(page.getByTestId('event-detail').first()).toContainText('run level')
  })

  test('replay reconstructs recorded state and offers no re-execution control', async ({ page }) => {
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)
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
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)
    await page.getByLabel('Search runs').fill('nothing-matches-this')
    await expect(page.getByText(/no runs match/i)).toBeVisible()

    await page.getByRole('button', { name: 'Reset filters' }).click()
    await expect(page.getByRole('button', { name: /fix the ingest pipeline/ }).first()).toBeVisible()
  })

  test('acknowledges an alert from the inspector', async ({ page }) => {
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    const acknowledge = page.getByRole('button', { name: 'Acknowledge' }).first()
    await expect(acknowledge).toBeVisible()
    await acknowledge.click()
    await expect(page.getByText(/resolved or acknowledged alert/)).toBeVisible()
  })
})

test.describe('keyboard and zoom', () => {
  test('reaches every run row with the keyboard and shows a visible focus ring', async ({ page }) => {
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)
    const row = page.getByRole('button', { name: /fix the ingest pipeline/ }).first()
    await row.focus()
    await expect(row).toBeFocused()
    const outline = await row.evaluate((node) => getComputedStyle(node).outlineWidth)
    expect(outline).not.toBe('0px')
  })

  test('reflows at a narrow viewport without hiding the run index', async ({ page }) => {

    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await page.setViewportSize({ width: 420, height: 900 })
    await openDashboard(page)
    await expect(page.getByRole('button', { name: /fix the ingest pipeline/ }).first()).toBeVisible()
  })
})

test.describe('security', () => {
  test('rejects an unauthenticated API request from the page context', async ({ page }) => {

    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await page.context().clearCookies()
    const response = await page.request.get('/api/v1/runs', { failOnStatusCode: false })
    expect(response.status()).toBe(401)
    const body = await response.text()
    expect(body).not.toContain(E2E_TOKEN)
  })

  test('loads no external script, font, or image', async ({ page }) => {

    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    const external = trackExternalRequests(page)
    await openDashboard(page)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    await expect(page.getByText('5 events loaded')).toBeVisible()
    expect(external).toEqual([])
  })

  test('renders a hostile captured payload as inert text', async ({ page }) => {
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)
    await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
    // A captured payload is rendered as text inside a detail panel, never as
    // markup, and it never executes.
    await page.getByRole('button', { name: /#4/ }).first().click()
    const detail = page.getByTestId('event-detail').first()
    await expect(detail).toBeVisible()
    expect(await detail.locator('script, img, svg, iframe').count()).toBe(0)
    expect(await page.evaluate(() => window.__pwned)).toBeUndefined()
  })

  test('never renders a token into the document', async ({ page }) => {
    test.skip(!E2E_TOKEN, 'TRACEBOARD_E2E_TOKEN is not set')

    await openDashboard(page)
    await page.goto('#/settings')
    await expect(page.getByText('Listen address')).toBeVisible()
    const text = await page.locator('body').innerText()
    expect(text).not.toContain(E2E_TOKEN)
  })
})
