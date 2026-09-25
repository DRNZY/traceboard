# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: run-inspector.spec.ts >> run index and inspector >> signs in and shows the captured run without a manual reload
- Location: e2e/run-inspector.spec.ts:34:3

# Error details

```
Test timeout of 60000ms exceeded.
```

```
Error: page.waitForURL: Test timeout of 60000ms exceeded.
=========================== logs ===========================
waiting for navigation until "load"
============================================================
```

# Page snapshot

```yaml
- main [ref=e2]:
  - heading "Traceboard sign in" [level=1] [ref=e3]
  - generic [ref=e4]:
    - text: One-time token
    - textbox "One-time token" [ref=e5]: FKuGpagvrV-Q2XBEbTQrbNZn_EA_Iu2inWzdxkKd_EI
    - button "Open dashboard" [active] [ref=e6]
```

# Test source

```ts
  1   | import { expect, test, type Page } from '@playwright/test'
  2   | 
  3   | /**
  4   |  * The end-to-end suite drives the real collector. `globalSetup` starts the
  5   |  * binary against a throwaway home, signs in once, and hands every worker the
  6   |  * same session cookie, so the specs read like an operator's workflow.
  7   |  */
  8   | export const BASE_URL = process.env.TRACEBOARD_E2E_URL ?? 'http://127.0.0.1:47821'
  9   | const E2E_TOKEN = process.env.TRACEBOARD_E2E_TOKEN ?? ''
  10  | 
  11  | declare global {
  12  |   interface Window {
  13  |     __pwned?: unknown
  14  |   }
  15  | }
  16  | 
  17  | async function signIn(page: Page, token: string): Promise<void> {
  18  |   await page.goto(`/auth/signin?token=${token}`)
  19  |   await page.getByRole('button', { name: 'Open dashboard' }).click()
> 20  |   await page.waitForURL((url) => !url.pathname.startsWith('/auth'))
      |              ^ Error: page.waitForURL: Test timeout of 60000ms exceeded.
  21  | }
  22  | 
  23  | function trackExternalRequests(page: Page): string[] {
  24  |   const external: string[] = []
  25  |   page.on('request', (request) => {
  26  |     const url = request.url()
  27  |     if (url.startsWith(BASE_URL) || url.startsWith('data:') || url.startsWith('blob:')) return
  28  |     external.push(url)
  29  |   })
  30  |   return external
  31  | }
  32  | 
  33  | test.describe('run index and inspector', () => {
  34  |   test('signs in and shows the captured run without a manual reload', async ({ page }) => {
  35  |     const token = E2E_TOKEN
  36  |     test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')
  37  | 
  38  |     await signIn(page, token)
  39  | 
  40  |     const row = page.getByRole('button', { name: /fix the ingest pipeline/ }).first()
  41  |     await expect(row).toBeVisible()
  42  |     await row.click()
  43  | 
  44  |     await expect(page.getByRole('heading', { name: 'Run inspector' })).toBeVisible()
  45  |     await expect(page.getByText('fix the ingest pipeline').first()).toBeVisible()
  46  |     await expect(page.getByText('5 events loaded')).toBeVisible()
  47  |   })
  48  | 
  49  |   test('shows an event detail with redacted attributes and a run level marker', async ({ page }) => {
  50  |     const token = E2E_TOKEN
  51  |     test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')
  52  | 
  53  |     await signIn(page, token)
  54  |     await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
  55  |     await expect(page.getByText('5 events loaded')).toBeVisible()
  56  | 
  57  |     await page.getByRole('button', { name: /#3/ }).first().click()
  58  |     const detail = page.getByTestId('event-detail').first()
  59  |     await expect(detail).toBeVisible()
  60  |     await expect(detail).toContainText('[REDACTED:api_key]')
  61  |     await expect(detail).toContainText('1 field redacted before storage.')
  62  |   })
  63  | 
  64  |   test('replay reconstructs recorded state and offers no re-execution control', async ({ page }) => {
  65  |     const token = E2E_TOKEN
  66  |     test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')
  67  | 
  68  |     await signIn(page, token)
  69  |     await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
  70  |     await expect(page.getByText('5 events loaded')).toBeVisible()
  71  | 
  72  |     const slider = page.getByLabel('Replay position')
  73  |     await expect(slider).toBeVisible()
  74  |     await slider.fill('1')
  75  |     await expect(page.getByTestId('replay-state')).toContainText('2 of 5')
  76  | 
  77  |     const labels = await page.getByRole('button').allTextContents()
  78  |     expect(labels.join(' ')).not.toMatch(/re-?run|re-?execute|retry|resume/i)
  79  |   })
  80  | 
  81  |   test('filters the run index through the server', async ({ page }) => {
  82  |     const token = E2E_TOKEN
  83  |     test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')
  84  | 
  85  |     await signIn(page, token)
  86  |     await page.getByLabel('Search runs').fill('nothing-matches-this')
  87  |     await expect(page.getByText(/no runs match/i)).toBeVisible()
  88  | 
  89  |     await page.getByRole('button', { name: 'Reset filters' }).click()
  90  |     await expect(page.getByRole('button', { name: /fix the ingest pipeline/ }).first()).toBeVisible()
  91  |   })
  92  | 
  93  |   test('acknowledges an alert from the inspector', async ({ page }) => {
  94  |     const token = E2E_TOKEN
  95  |     test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')
  96  | 
  97  |     await signIn(page, token)
  98  |     await page.getByRole('button', { name: /fix the ingest pipeline/ }).first().click()
  99  |     const acknowledge = page.getByRole('button', { name: 'Acknowledge' }).first()
  100 |     await expect(acknowledge).toBeVisible()
  101 |     await acknowledge.click()
  102 |     await expect(page.getByText(/resolved or acknowledged alert/)).toBeVisible()
  103 |   })
  104 | })
  105 | 
  106 | test.describe('keyboard and zoom', () => {
  107 |   test('reaches every run row with the keyboard and shows a visible focus ring', async ({ page }) => {
  108 |     const token = E2E_TOKEN
  109 |     test.skip(!token, 'TRACEBOARD_E2E_TOKEN is not set')
  110 | 
  111 |     await signIn(page, token)
  112 |     const row = page.getByRole('button', { name: /fix the ingest pipeline/ }).first()
  113 |     await row.focus()
  114 |     await expect(row).toBeFocused()
  115 |     const outline = await row.evaluate((node) => getComputedStyle(node).outlineWidth)
  116 |     expect(outline).not.toBe('0px')
  117 |   })
  118 | 
  119 |   test('reflows at a narrow viewport without hiding the run index', async ({ page }) => {
  120 |     const token = E2E_TOKEN
```