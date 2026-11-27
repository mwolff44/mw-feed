// SPDX-License-Identifier: GPL-3.0-or-later
const { test, expect, setConfig } = require('../support/fixtures');

test.use({ feedItems: 4 });

test('a asks for confirmation, then marks every item read', async ({ signedIn: page }) => {
  const dialogs = [];
  page.once('dialog', (d) => { dialogs.push(d.message()); d.accept(); });

  await page.keyboard.press('a');

  await expect(page.locator('.reading h1 small')).toHaveText('0 unread');
  expect(dialogs).toEqual(['Mark all these items as read?']);
  await expect(page.locator('details.item')).toHaveCount(0);
});

test('refusing the confirmation changes nothing', async ({ signedIn: page }) => {
  page.once('dialog', (d) => d.dismiss());
  await page.evaluate(() => { window.__stillHere = true; });

  await page.keyboard.press('a');
  await page.waitForTimeout(300);

  await expect(page.locator('details.item')).toHaveCount(4);
  expect(await page.evaluate(() => window.__stillHere === true)).toBe(true);
});

const items = (page) => page.locator('.reading .item');

test.describe('item keys', () => {
  test.use({ feedItems: 4 });

  test('space and t open and close the current item', async ({ signedIn: page }) => {
    await page.keyboard.press(' ');
    await expect(items(page).first()).toHaveAttribute('open', '');
    await page.keyboard.press('t');
    await expect(items(page).first()).not.toHaveAttribute('open', '');
    expect(await page.evaluate(() => scrollY)).toBe(0); // space did not scroll the page
  });

  test('space keeps its native meaning on a focused summary', async ({ signedIn: page }) => {
    await items(page).first().locator('summary').focus();
    await page.keyboard.press(' ');
    await expect(items(page).first()).toHaveAttribute('open', ''); // toggled once, not twice
  });

  test('m marks the current item read without opening it, shift+m also opens it', async ({ signedIn: page }) => {
    await page.keyboard.press('m');
    await expect(page.locator('.reading h1 small')).toHaveText('3 unread');
    await expect(items(page).first()).not.toHaveClass(/\bunread\b/);
    await expect(items(page).first()).not.toHaveAttribute('open', '');
    await expect(items(page).first().locator('form[action$="/unread"]')).toHaveCount(1);

    await page.keyboard.press('Shift+M'); // back to unread, and opens
    await expect(page.locator('.reading h1 small')).toHaveText('4 unread');
    await expect(items(page).first()).toHaveAttribute('open', '');
  });

  test('* stars and unstars the current item', async ({ signedIn: page }) => {
    await page.keyboard.press('*');
    await expect(items(page).first().locator('form[action$="/unstar"]')).toHaveCount(1);
    await page.keyboard.press('*');
    await expect(items(page).first().locator('form[action$="/star"]')).toHaveCount(1);
  });

  test('o opens the link in a new tab and shift+o in the current one', async ({ signedIn: page, context }) => {
    await context.route('https://site.example/**', (route) => route.fulfill({ contentType: 'text/html', body: 'ok' }));
    const popup = page.waitForEvent('popup');
    await page.keyboard.press('o');
    expect((await popup).url()).toBe('https://site.example/4');
    expect(page.url()).not.toContain('site.example');

    await page.keyboard.press('Shift+O');
    await page.waitForURL('https://site.example/4');
  });
});

test.describe('page keys', () => {
  test.use({ feedItems: 5 });

  test('shift+n and shift+p change page', async ({ signedIn: page, app }) => {
    await setConfig(page, app, { by_page: 2 });

    await page.keyboard.press('Shift+N');
    await expect(page).toHaveURL(/page=2/);
    await expect(items(page).first()).toContainText('Item 3');
    await page.keyboard.press('Shift+P');
    await expect(page).toHaveURL(/page=1/);
    await expect(items(page).first()).toContainText('Item 5');
  });
});

test.describe('menu keys', () => {
  test.use({ feedItems: 2 });

  test('h goes home, from any page', async ({ signedIn: page, app }) => {
    await page.goto(`${app.url}/config`);
    await page.keyboard.press('h');
    await expect(page).toHaveURL(`${app.url}/`);
  });

  test('? and F1 open the help', async ({ signedIn: page, app }) => {
    await page.keyboard.press('?');
    await expect(page).toHaveURL(`${app.url}/help`);
    await page.goto(app.url);
    await page.keyboard.press('F1');
    await expect(page).toHaveURL(`${app.url}/help`);
  });

  test('v changes the view and f hides the feed list', async ({ signedIn: page }) => {
    await page.keyboard.press('v');
    await expect(page.locator('article.item')).toHaveCount(2);
    await page.keyboard.press('f');
    await expect(page.locator('nav.feeds')).toHaveCount(0);
  });

  test('e opens the edit page of the selection', async ({ signedIn: page, app }) => {
    await page.keyboard.press('e');
    await expect(page).toHaveURL(`${app.url}/edit`);
  });

  test('u updates the selection and r reloads the page', async ({ signedIn: page }) => {
    await page.keyboard.press('u');
    await expect(page.locator('.flash')).toBeVisible();
    await page.evaluate(() => { window.__stillHere = true; });
    await Promise.all([page.waitForNavigation(), page.keyboard.press('r')]);
    expect(await page.evaluate(() => window.__stillHere === true)).toBe(false);
  });

  test('keys of a control that the page does not have do nothing', async ({ signedIn: page, app }) => {
    await page.goto(`${app.url}/starred`);
    await page.keyboard.press('e');
    await page.keyboard.press('u');
    await page.keyboard.press('a');
    await page.waitForTimeout(200);
    await expect(page).toHaveURL(`${app.url}/starred`);
  });
});
