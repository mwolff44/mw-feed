// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect, setConfig } = require('../support/fixtures');

test.use({ feedItems: 40 });

async function pressN(page, times) {
  for (let i = 0; i < times; i++) await page.keyboard.press('n');
}

test('with autofocus the current item is scrolled into view', async ({ signedIn: page, app }) => {
  await setConfig(page, app, { autofocus: true, by_page: 40 });

  await pressN(page, 39);

  await expect(page.locator('.reading .item.current')).toBeInViewport();
  expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
  expect(page.url()).not.toContain('#'); // the address bar and the history are left alone
});

test('without autofocus the page does not scroll', async ({ signedIn: page, app }) => {
  await setConfig(page, app, { autofocus: false, by_page: 40 });

  await pressN(page, 39);

  await expect(page.locator('.reading .item.current')).not.toBeInViewport();
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
});
