// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect, setConfig } = require('../support/fixtures');

const items = (page) => page.locator('.reading .item');
const unread = (page) => page.locator('.reading h1 small');

test.describe('moving with the keyboard', () => {
  test.use({ feedItems: 4 });

  test('n marks the item that is left as read', async ({ signedIn: page, app }) => {
    await setConfig(page, app, { autoread_item: true });

    await page.keyboard.press('n');

    await expect(unread(page)).toHaveText('3 unread');
    await expect(items(page).first().locator('form[action$="/unread"]')).toHaveCount(1);
    await page.keyboard.press('j');
    await expect(unread(page)).toHaveText('2 unread');
  });

  test('going back marks nothing', async ({ signedIn: page, app }) => {
    await setConfig(page, app, { autoread_item: true });

    await page.keyboard.press('n'); // item 4 read
    await expect(unread(page)).toHaveText('3 unread');
    await page.keyboard.press('p'); // leaves item 3, unread
    await page.keyboard.press('k');
    await page.waitForTimeout(300);

    await expect(unread(page)).toHaveText('3 unread');
  });

  test('with the setting off nothing is marked', async ({ signedIn: page, app }) => {
    await setConfig(page, app, { autoread_item: false });

    await page.keyboard.press('n');
    await page.keyboard.press('n');
    await page.waitForTimeout(300);

    await expect(unread(page)).toHaveText('4 unread');
  });

  test('past the last item of a page the marking is done before the next page loads', async ({ signedIn: page, app }) => {
    await setConfig(page, app, { autoread_item: true, by_page: 2 });

    await page.keyboard.press('n'); // item 4 read
    await page.keyboard.press('n'); // item 3 read, then next page
    await expect(page).toHaveURL(/page=2/);

    await expect(unread(page)).toHaveText('2 unread'); // 4 and 3 were marked before leaving
  });
});

test.describe('swiping', () => {
  test.use({ feedItems: 4, hasTouch: true });

  test('a swipe to the left marks the item that is left as read', async ({ signedIn: page, app }) => {
    await setConfig(page, app, { autoread_item: true });
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 300, y: 400 }] });
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 150, y: 400 }] });
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });

    await expect(unread(page)).toHaveText('3 unread');
  });
});
