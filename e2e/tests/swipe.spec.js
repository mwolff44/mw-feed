// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect, setConfig } = require('../support/fixtures');

test.use({ feedItems: 5, hasTouch: true });

const items = (page) => page.locator('.reading .item');

// swipe sends one finger from (x1, y1) to (x2, y2) in `ms` milliseconds.
async function swipe(page, [x1, y1], [x2, y2], ms = 100) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: x1, y: y1 }] });
  await page.waitForTimeout(ms / 2);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: x2, y: y2 }] });
  await page.waitForTimeout(ms / 2);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

test('swiping left goes to the next item and right to the previous one', async ({ signedIn: page }) => {
  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);

  await swipe(page, [300, 400], [150, 410]);
  await expect(items(page).nth(1)).toHaveClass(/\bcurrent\b/);

  await swipe(page, [150, 400], [300, 395]);
  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);
});

test('a slow, short or vertical gesture is not a swipe', async ({ signedIn: page }) => {
  await swipe(page, [300, 400], [150, 400], 700); // too slow
  await swipe(page, [300, 400], [280, 400]); // too short
  await swipe(page, [300, 200], [150, 400]); // too vertical
  await page.waitForTimeout(200);

  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);
});

test('with the swipe setting off nothing happens', async ({ signedIn: page, app }) => {
  await setConfig(page, app, { swipe: false });

  await swipe(page, [300, 400], [150, 400]);
  await page.waitForTimeout(200);

  await expect(items(page).first()).toHaveClass(/\bcurrent\b/);
});
