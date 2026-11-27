// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect, setConfig } = require('../support/fixtures');

// The newest item comes first: with 5 items the page lists Item 5 … Item 1.
test.use({ feedItems: 5 });

const items = (page) => page.locator('.reading .item');
const current = (page) => page.locator('.reading .item.current');

test('the first item is current when the page loads, and a click on another makes it current', async ({ signedIn: page }) => {
  await expect(current(page)).toHaveCount(1);
  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);
  await expect(items(page).first()).toHaveAttribute('aria-current', 'true');

  await items(page).nth(2).locator('summary').click();

  await expect(current(page)).toHaveCount(1);
  await expect(items(page).nth(2)).toHaveClass(/\bcurrent\b/);
  await expect(items(page).first()).not.toHaveAttribute('aria-current', 'true');
});

test('n and p move the current item without opening it', async ({ signedIn: page }) => {
  await page.keyboard.press('n');
  await expect(items(page).nth(1)).toHaveClass(/\bcurrent\b/);
  await page.keyboard.press('ArrowRight');
  await expect(items(page).nth(2)).toHaveClass(/\bcurrent\b/);
  await page.keyboard.press('p');
  await expect(items(page).nth(1)).toHaveClass(/\bcurrent\b/);
  await page.keyboard.press('ArrowLeft');
  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);
  await expect(page.locator('.reading .item[open]')).toHaveCount(0);
});

test('j and k move and open the item, closing the one that is left', async ({ signedIn: page }) => {
  await items(page).first().locator('summary').click();
  await expect(items(page).first()).toHaveAttribute('open', '');

  await page.keyboard.press('j');
  await expect(items(page).nth(1)).toHaveAttribute('open', '');
  await expect(items(page).first()).not.toHaveAttribute('open', '');
  await expect(items(page).nth(1)).toHaveClass(/\bcurrent\b/);

  await page.keyboard.press('k');
  await expect(items(page).first()).toHaveAttribute('open', '');
  await expect(items(page).nth(1)).not.toHaveAttribute('open', '');
});

test('keys typed in a form field do not move anything', async ({ signedIn: page, app }) => {
  await page.goto(`${app.url}/add`);
  await page.fill('input[name=url]', 'jjnnpp');
  await expect(page.locator('input[name=url]')).toHaveValue('jjnnpp');
});

test('past the last item n goes to the next page, and p from there comes back on its last item', async ({ signedIn: page, app }) => {
  await setConfig(page, app, { by_page: 2 }); // pages: [5 4] [3 2] [1]
  await expect(items(page)).toHaveCount(2);

  await page.keyboard.press('n'); // item 4
  await page.keyboard.press('n'); // past the last: next page
  await expect(page).toHaveURL(/[?&]page=2\b/);
  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);
  await expect(items(page).first()).toContainText('Item 3');

  await page.keyboard.press('p'); // before the first: previous page, on its last item
  await expect(page).not.toHaveURL(/[?&]page=2\b/);
  await expect(items(page).last()).toHaveClass(/\bcurrent\b/);
  await expect(items(page).last()).toContainText('Item 4');

  await page.keyboard.press('p');
  await page.keyboard.press('p'); // nowhere to go before the first page
  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);
});
