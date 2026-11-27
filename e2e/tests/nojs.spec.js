// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect } = require('../support/fixtures');

test.use({ javaScriptEnabled: false, feedItems: 3 });

test('without JavaScript an item opens, and actions work by reloading the page', async ({ signedIn: page }) => {
  await expect(page.locator('html')).not.toHaveClass(/\bjs\b/);
  const first = page.locator('details.item').first();

  await first.locator('summary').click(); // native <details>
  await expect(first).toHaveAttribute('open', '');

  await first.locator('form[action$="/star"] button').click();

  await expect(page).toHaveURL(/open=\d+#item-\d+$/);
  const reopened = page.locator('details.item[open]');
  await expect(reopened).toHaveCount(1); // the server reopened the item
  await expect(reopened.locator('form[action$="/unstar"]')).toHaveCount(1);

  await reopened.locator('form[action$="/read"] button').click();
  await expect(page.locator('.reading h1 small')).toHaveText('2 unread');
});

test('without JavaScript the menu works with plain forms', async ({ signedIn: page }) => {
  await page.locator('form[action="/display"] button[name=view]').click();
  await expect(page.locator('article.item')).toHaveCount(3);
});
