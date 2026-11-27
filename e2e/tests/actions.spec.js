// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect } = require('../support/fixtures');

// A marker set on the window disappears if the page is reloaded: it proves that
// an action did not navigate.
async function openNewest(page) {
  await page.evaluate(() => { window.__stillHere = true; });
  const item = page.locator('details.item').first();
  await item.locator('summary').click();
  return item;
}
const stillHere = (page) => page.evaluate(() => window.__stillHere === true);

test('marking an item read updates it and the counters without reloading', async ({ signedIn: page }) => {
  await expect(page.locator('h1 small')).toHaveText('3 unread');
  const item = await openNewest(page);
  await expect(item).toHaveClass(/\bunread\b/);

  await item.locator('form[action$="/read"] button').click();

  await expect(item.locator('form[action$="/unread"] button')).toBeVisible();
  await expect(item).not.toHaveClass(/\bunread\b/);
  await expect(item).toHaveAttribute('open', '');
  await expect(page.locator('h1 small')).toHaveText('2 unread');
  await expect(page.locator('.feeds li.all .count')).toHaveText('2');
  expect(await stillHere(page)).toBe(true);

  await item.locator('form[action$="/unread"] button').click();
  await expect(item).toHaveClass(/\bunread\b/);
  await expect(page.locator('h1 small')).toHaveText('3 unread');
});

test('starring an item toggles the star without reloading', async ({ signedIn: page }) => {
  const item = await openNewest(page);

  await item.locator('form[action$="/star"] button').click();

  await expect(item.locator('form[action$="/unstar"] button')).toBeVisible();
  await expect(item).toHaveAttribute('open', '');
  expect(await stillHere(page)).toBe(true);
});

test('when the fragment cannot be fetched the form is sent normally', async ({ signedIn: page }) => {
  await page.route(/\/item\/\d+\?/, (route) => route.abort());
  const item = await openNewest(page);

  await item.locator('form[action$="/read"] button').click();

  // The page was reloaded by the ordinary form submission, and the action took effect.
  await expect(page.locator('h1 small')).toHaveText('2 unread');
  expect(await stillHere(page)).toBe(false);
  await expect(page.locator('details.item')).toHaveCount(2); // the unread filter hides the item once reloaded
});

test('the item that an action replaced is still the current one', async ({ signedIn: page }) => {
  await page.keyboard.press('n'); // second item becomes current
  const second = page.locator('details.item').nth(1);
  await second.locator('summary').click();

  await second.locator('form[action$="/star"] button').click();
  await expect(page.locator('details.item').nth(1).locator('form[action$="/unstar"] button')).toBeVisible();

  await expect(page.locator('details.item.current')).toHaveCount(1);
  await page.keyboard.press('n');
  await expect(page.locator('details.item').nth(2)).toHaveClass(/\bcurrent\b/);
});
