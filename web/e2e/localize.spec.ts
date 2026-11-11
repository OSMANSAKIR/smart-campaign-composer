// B-3 end to end (mock LLM): the official brief reaches Turkish and English readers; Claude adapts the Turkish
// copy to English as a separate step; editing the Turkish copy makes the English one stale until it is
// re-adapted; the campaign is approved "each recipient in their language" and the dashboard shows the
// delivery per language.
import { expect, test } from '@playwright/test'

test('localize: write the English version, stale after a primary edit, re-adapt, send per language [B-3]', async ({
  page,
  request,
}) => {
  // Audience: the language breakdown is shown.
  await page.goto('/#/new')
  await page.getByRole('button', { name: 'Find audience' }).click()
  const table = page.getByRole('table', { name: 'People who can be reached, by the language they read' })
  await expect(table.getByRole('row', { name: /^Turkish/ })).toBeVisible()
  const enRow = table.getByRole('row', { name: /^English/ })
  await expect(enRow).toBeVisible()
  const enPeople = Number((await enRow.getByRole('cell').first().innerText()).replace(/\D/g, ''))
  expect(enPeople).toBeGreaterThan(0)
  const campaignId = page.url().match(/#\/c\/([0-9a-f-]{36})/)?.[1]
  expect(campaignId).toBeTruthy()

  // Content in the campaign's language first.
  await page.getByRole('button', { name: 'Write the content' }).click()
  await page.getByRole('button', { name: 'Write with Claude' }).click()
  await expect(page.getByText(/^Saved as version 1\./)).toBeVisible()

  // The other language is a separate step, offered with the number of people who read it.
  const write = page.getByRole('button', { name: `Write the English version (${enPeople} recipients)` })
  await expect(write).toBeEnabled()
  const localize = page.waitForResponse((r) => r.url().endsWith('/content/localize'))
  await write.click()
  expect((await localize).headers()['content-type']).toContain('text/event-stream')
  await expect(page.getByText(/^Saved as version 2\./)).toBeVisible()

  // TR | EN tabs per variant: the English copy is there and editable; the Turkish copy did not change.
  const cardA = page.locator('[data-variant="a"]')
  await expect(cardA.getByRole('tab')).toHaveText([/^TR/, /^EN/])
  // The card opens on the English tab while Claude writes it, and stays there.
  await expect(cardA.getByRole('tab', { name: 'English version of variant A' })).toHaveAttribute('aria-selected', 'true')
  await cardA.getByRole('tab', { name: 'Turkish version of variant A' }).click()
  const trSms = cardA.getByRole('textbox', { name: 'SMS text, variant A' })
  const turkish = await trSms.inputValue()
  expect(turkish).toMatch(/^Merhaba/)
  await cardA.getByRole('tab', { name: 'English version of variant A' }).click()
  const enSms = cardA.getByRole('textbox', { name: 'SMS text, variant A (English)' })
  await expect(enSms).toHaveValue(/^Hi \{\{first_name\}\}/)
  await expect(enSms).toBeEditable()
  expect(await enSms.inputValue()).not.toBe(turkish)
  await expect(cardA.getByText('out of date')).toHaveCount(0)

  // Editing the Turkish copy makes the English copy out of date at once; saving keeps it so.
  await cardA.getByRole('tab', { name: 'Turkish version of variant A' }).click()
  await trSms.fill('Merhaba {{first_name}}, sepetin seni bekliyor! %30 indirim bugün senin.')
  await expect(cardA.getByRole('tab', { name: 'English version of variant A' })).toContainText('out of date')
  await page.getByRole('button', { name: 'Save changes' }).click()
  await expect(page.getByText(/^Saved as version 3\./)).toBeVisible()
  await cardA.getByRole('tab', { name: 'English version of variant A' }).click()
  await expect(cardA.getByText('The Turkish text changed after this version was written.')).toBeVisible()

  // Approve is refused for "each recipient in their language" while it is stale.
  await page.getByRole('button', { name: 'Continue to approve' }).click()
  await expect(page.getByRole('radio', { name: 'Each recipient in their language' })).toBeDisabled()
  await expect(page.getByRole('radio', { name: 'Everyone in Turkish' })).toBeChecked()
  await expect(page.locator('#language-note')).toContainText('out of date')

  // Re-adapt from the content step; the notice goes away and the new version is current.
  await page.getByRole('button', { name: 'Back to content' }).click()
  await cardA.getByRole('tab', { name: 'English version of variant A' }).click()
  await cardA.getByRole('button', { name: 'Re-adapt to English' }).click()
  await expect(page.getByText(/^Saved as version 4\./)).toBeVisible()
  await expect(cardA.getByText('The Turkish text changed after this version was written.')).toHaveCount(0)
  await expect(cardA.getByRole('tab', { name: 'English version of variant A' })).not.toContainText('out of date')

  // Approve: each recipient in their language is the default now that it is valid.
  await page.getByRole('button', { name: 'Continue to approve' }).click()
  await expect(page.getByRole('radio', { name: 'Each recipient in their language' })).toBeChecked()
  await page.getByRole('radio', { name: 'Send A' }).check()
  await page.getByRole('button', { name: 'Review and approve' }).click()
  const dialog = page.getByRole('dialog', { name: 'Send this campaign?' })
  await expect(dialog).toContainText('Each recipient in their own language')
  await dialog.getByRole('button', { name: 'Approve and send' }).click()

  // Dashboard: the per-language table, and the API agrees that the languages add up.
  await expect(page).toHaveURL(new RegExp(`#/c/${campaignId}/live$`))
  await expect(page.getByText('Finished', { exact: true })).toBeVisible({ timeout: 90_000 })
  const smsByLanguage = page.getByRole('table', { name: 'SMS delivery by language' })
  await expect(smsByLanguage.getByRole('row', { name: /^Turkish/ })).toBeVisible()
  await expect(smsByLanguage.getByRole('row', { name: /^English/ })).toBeVisible()

  const c = await (await request.get(`/api/campaigns/${campaignId}`)).json()
  expect(c.language_mode).toBe('recipient')
  expect(c.approved_content_version).toBe(4)
  for (const ch of ['sms', 'email'] as const) {
    const p = c.progress.channels[ch]
    const sum = Object.values(p.locales as Record<string, { total: number; sent: number }>).reduce((n, l) => n + l.total, 0)
    expect(sum, `${ch}: per-language totals add up`).toBe(p.total)
    expect(p.locales.en.total, `${ch}: English recipients`).toBeGreaterThan(0)
    expect(p.locales.en.sent).toBeGreaterThan(0)
    expect(p.locales.tr.sent).toBeGreaterThan(0)
  }
})
