// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect } = require('../support/fixtures');

// The item links point to a site that does not exist: answer every request ourselves.
async function fakeSite(context) {
  await context.route('https://site.example/**', (route) => route.fulfill({ contentType: 'text/html', body: 'ok' }));
}


test.describe('with 4 items', () => {
  test.use({ feedItems: 4 });

  test('z opens every unread item in a tab and marks them read', async ({ signedIn: page, context }) => {
    await fakeSite(context);
    const pages = [];
    context.on('page', (p) => pages.push(p));

    await page.keyboard.press('z');

    await expect(page.locator('.reading h1 small')).toHaveText('0 unread');
    await expect.poll(() => pages.length).toBe(4);
    const urls = [];
    for (const p of pages) { await p.waitForLoadState(); urls.push(p.url()); }
    expect(urls.sort()).toEqual([1, 2, 3, 4].map((n) => `https://site.example/${n}`));
  });

  test('when the browser blocks a tab, that item and the next ones stay unread', async ({ signedIn: page, context }) => {
    await fakeSite(context);
    await page.evaluate(() => {
      const real = window.open;
      let calls = 0;
      window.open = (...args) => (++calls > 2 ? null : real.apply(window, args));
    });

    await page.keyboard.press('z');

    await expect(page.locator('.reading h1 small')).toHaveText('2 unread');
  });

  test('items that are already read are not opened again', async ({ signedIn: page, context }) => {
    await fakeSite(context);
    await page.locator('details.item').first().locator('summary').click();
    await page.locator('details.item').first().locator('form[action$="/read"] button').click();
    await expect(page.locator('.reading h1 small')).toHaveText('3 unread');
    const pages = [];
    context.on('page', (p) => pages.push(p));

    await page.keyboard.press('z');

    await expect(page.locator('.reading h1 small')).toHaveText('0 unread');
    await expect.poll(() => pages.length).toBe(3);
  });
});

test.describe('with 12 items', () => {
  test.use({ feedItems: 12 });

  test('z opens at most 10 tabs at a time', async ({ signedIn: page, context }) => {
    await fakeSite(context);
    const pages = [];
    context.on('page', (p) => pages.push(p));
    await page.evaluate(() => { window.__stillHere = true; });

    await page.keyboard.press('z');
    await expect(page.locator('.reading h1 small')).toHaveText('2 unread');
    await expect.poll(() => pages.length).toBe(10);
  });
});
