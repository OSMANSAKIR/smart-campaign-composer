// End-to-end happy path (KICKOFF §8, P1): the official brief → audience → streamed content → edit → approve →
// dashboard reaches a terminal state. Runs against the real API, worker and mock provider with the mock LLM.
import { expect, test, type Page } from '@playwright/test'

const OFFICIAL_BRIEF =
  'Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul’daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun.'

// Samples the SMS field every 25 ms until the stream has finished, and returns the distinct values seen.
// More than one non-empty value means the text arrived in pieces, not in one final paint.
async function watchSmsWhileStreaming(page: Page): Promise<string[]> {
  return page.evaluate(
    () =>
      new Promise<string[]>((resolve) => {
        const seen: string[] = []
        const started = Date.now()
        const timer = setInterval(() => {
          const el = document.querySelector<HTMLTextAreaElement>('#sms-text-a')
          const v = el?.value ?? ''
          if (v && seen[seen.length - 1] !== v) seen.push(v)
          const done = document.body.innerText.includes('Saved as version 1')
          if (done || Date.now() - started > 30_000) {
            clearInterval(timer)
            resolve(seen)
          }
        }, 25)
      }),
  )
}

test('official brief: audience, streamed content, edit, approve, delivery completes [R-FLOW-1..5]', async ({
  page,
  request,
}) => {
  // 1. Brief (R-FLOW-1): the official example is the default text.
  await page.goto('/#/new')
  await expect(page.getByRole('status').filter({ hasText: 'Mock LLM' })).toBeVisible()
  const brief = page.getByRole('textbox', { name: /^Brief/ })
  await expect(brief).toHaveValue(OFFICIAL_BRIEF)
  await page.getByRole('button', { name: 'Find audience' }).click()

  // 2. Audience: the validated rule is rendered in words, with counts from the database.
  const rules = page.getByRole('list', { name: 'Audience rules' })
  await expect(rules).toContainText('İstanbul')
  await expect(rules).toContainText('30')
  const size = page.getByRole('group', { name: 'Audience size' })
  const matched = Number((await size.locator('text=people match').locator('..').innerText()).replace(/\D/g, ''))
  expect(matched).toBeGreaterThan(0)
  const campaignId = page.url().match(/#\/c\/([0-9a-f-]{36})/)?.[1]
  expect(campaignId).toBeTruthy()

  // 3. Content streams in (R-FLOW-2) and is validated by the server.
  await page.getByRole('button', { name: 'Write the content' }).click()
  const generate = page.waitForResponse((r) => r.url().endsWith('/content/generate'))
  await page.getByRole('button', { name: 'Write with Claude' }).click()
  const watching = watchSmsWhileStreaming(page)
  expect((await generate).headers()['content-type']).toContain('text/event-stream')
  const values = await watching
  expect(values.length, `SMS field values seen while streaming: ${JSON.stringify(values)}`).toBeGreaterThan(1)
  const sms = page.getByRole('textbox', { name: 'SMS text, variant A' })
  await expect(sms).toHaveValue(/\{\{first_name\}\}/)
  // Two variants stream in (B-2): the second card is filled too, and both are valid.
  await expect(page.getByRole('textbox', { name: 'SMS text, variant B' })).toHaveValue(/\S/)
  await expect(page.getByText('Saved as version 1. Both variants are valid for SMS and email.')).toBeVisible()

  // 4. Edit (R-FLOW-3, R-CNT-3): an unknown variable is an error and blocks approval…
  await sms.fill('Merhaba {{first_name}}, kodun {{coupon}} seni bekliyor!')
  await expect(page.getByText(/unknown variable/i).first()).toBeVisible()
  await expect(page.getByRole('button', { name: 'Continue to approve' })).toBeDisabled()
  // …a valid edit is measured with the longest name and city in this audience (R-CNT-1), then saved.
  await sms.fill('Merhaba {{first_name}}, sepetin seni bekliyor! {{city}} için %30 indirim bugün geçerli.')
  await expect(page.getByText('Unsaved changes. Both variants are valid for SMS and email.')).toBeVisible()
  await expect(page.getByText(/\/ 160 with longest name and city/).first()).toBeVisible()
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText('Saved as version 2. Both variants are valid for SMS and email.')).toBeVisible()

  // 5. Approve behind a confirmation dialog.
  await page.getByRole('button', { name: 'Continue to approve' }).click()
  await expect(page.getByText('Version 2 of the content will be sent.')).toBeVisible()
  await page.getByRole('radio', { name: 'Send A' }).check() // B-2: one variant keeps the delivery counts simple
  await page.getByRole('button', { name: 'Review and approve' }).click()
  const dialog = page.getByRole('dialog', { name: 'Send this campaign?' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Approve and send' }).click()

  // 6. Dashboard (R-FLOW-4, R-FLOW-5, R-OBS-1): live progress until a terminal state.
  await expect(page).toHaveURL(new RegExp(`#/c/${campaignId}/live$`))
  await expect(page.getByRole('progressbar', { name: 'Overall delivery progress' })).toBeVisible()
  await expect(page.getByRole('img', { name: /^Throughput over the last two minutes/ })).toBeVisible()
  await expect(page.getByText('Finished', { exact: true })).toBeVisible({ timeout: 90_000 })
  await expect(page.getByText('100% · finished')).toBeVisible()

  // The API agrees: every queued message reached a final state, and version 2 was approved.
  const c = await (await request.get(`/api/campaigns/${campaignId}`)).json()
  expect(['completed', 'completed_with_failures']).toContain(c.status)
  for (const ch of ['sms', 'email'] as const) {
    const p = c.progress.channels[ch]
    expect(p.queued + p.sending + p.retry, `${ch} still in flight`).toBe(0)
    expect(p.sent + p.failed + p.skipped_no_consent, `${ch} totals`).toBe(p.total)
    expect(p.sent).toBeGreaterThan(0)
  }
  expect(c.approved_content_version).toBe(2)
  expect(c.send_variant).toBe('a')

  // 7. A reload reconnects and the first snapshot already holds the final state.
  await page.reload()
  await expect(page.getByText('100% · finished')).toBeVisible()
  await expect(page.getByText('Finished', { exact: true })).toBeVisible()
})

// F2 (N-WEB-001 B): the marketer edits Claude's rule in the catalog-driven editor; the count follows, the rule is
// saved, and the audience shows the "Edited" badge with Claude's original rule kept.
test('rule editor: change the city, the count changes, save, "Edited" badge [F2]', async ({ page }) => {
  await page.goto('/#/new')
  await page.getByRole('button', { name: 'Find audience' }).click()
  const rules = page.getByRole('list', { name: 'Audience rules' })
  await expect(rules).toContainText('İstanbul')

  const size = page.getByRole('group', { name: 'Audience size' })
  const matchedCount = async () =>
    Number((await size.locator('text=people match').locator('..').innerText()).replace(/\D/g, ''))
  const before = await matchedCount()

  await page.getByRole('button', { name: 'Edit rule' }).click()
  const editor = page.getByRole('region', { name: 'Edit the audience rule' })
  await expect(editor).toBeVisible()
  await expect(editor.getByTestId('preview-count')).toBeVisible()

  // Find the condition on the city field (the model's group order is not part of the contract).
  let group = 0
  for (let g = 1; g <= 5 && group === 0; g++) {
    const field = editor.getByRole('combobox', { name: `Condition 1 of group ${g}: field` })
    if ((await field.count()) > 0 && (await field.inputValue()) === 'city') group = g
  }
  expect(group, 'a group whose first condition is on city').toBeGreaterThan(0)
  await editor.getByRole('combobox', { name: `Condition 1 of group ${group}: value` }).selectOption('Ankara')
  await expect(editor.getByTestId('preview-count')).toBeVisible()

  await editor.getByRole('button', { name: 'Save rule' }).click()
  await expect(page.getByText('Edited', { exact: true })).toBeVisible()
  await expect(page.getByText('Claude’s original rule')).toBeVisible()
  await expect(rules).toContainText('Ankara')
  expect(await matchedCount()).not.toBe(before)
})
