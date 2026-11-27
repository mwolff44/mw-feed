// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect } = require('../support/fixtures');

test('the script runs on a reading page and an item opens without error', async ({ signedIn: page }) => {
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
  await page.reload();

  await expect(page.locator('html')).toHaveClass(/\bjs\b/);

  const item = page.locator('details.item').first();
  await item.locator('summary').click();
  await expect(item).toHaveAttribute('open', '');
  expect(errors).toEqual([]);
});
