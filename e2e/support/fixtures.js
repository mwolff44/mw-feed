// SPDX-License-Identifier: GPL-3.0-or-later
// Fixtures: a real mw-feed process with an empty database, a fake feed server,
// and a signed-in page that is subscribed to the fake feed.
const { test: base, expect } = require('@playwright/test');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

const binary = path.resolve(__dirname, '..', '.build', 'mw-feed');
const LOGIN = 'alice';
const PASSWORD = 'a sufficiently long passphrase';

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
    srv.on('error', reject);
  });
}

async function waitUntilUp(url) {
  for (let i = 0; i < 100; i++) {
    try {
      await fetch(url);
      return;
    } catch {
      await new Promise((r) => setTimeout(r, 50));
    }
  }
  throw new Error(`mw-feed did not start on ${url}`);
}

// rss builds a feed whose items are { title, link, date, content }.
function rss(title, items) {
  const entries = items
    .map((it) => `<item><title>${it.title}</title><link>${it.link}</link><pubDate>${it.date}</pubDate>` +
      `<description><![CDATA[${it.content ?? ''}]]></description></item>`)
    .join('');
  return `<?xml version="1.0"?><rss version="2.0"><channel><title>${title}</title>` +
    `<link>https://site.example/</link>${entries}</channel></rss>`;
}

// items(n) returns n items, the newest first in the page, with fixed dates.
function items(n) {
  return Array.from({ length: n }, (_, i) => ({
    title: `Item ${i + 1}`,
    link: `https://site.example/${i + 1}`,
    date: new Date(Date.UTC(2026, 0, 1 + i, 10)).toUTCString(),
    content: `<p>Body of item ${i + 1}</p>`,
  }));
}

const test = base.extend({
  // feedItems: how many items the fake feed has (override with test.use).
  feedItems: [3, { option: true }],

  // app: a fresh server and database.
  app: async ({}, use) => {
    const port = await freePort();
    const data = fs.mkdtempSync(path.join(os.tmpdir(), 'mw-feed-e2e-'));
    const proc = spawn(binary, ['serve'], {
      env: { ...process.env, MW_FEED_ADDR: `127.0.0.1:${port}`, MW_FEED_DATA: data, MW_FEED_ALLOW_PRIVATE: '1' },
      stdio: 'inherit',
    });
    const url = `http://127.0.0.1:${port}`;
    await waitUntilUp(url);
    await use({ url });
    proc.kill();
    fs.rmSync(data, { recursive: true, force: true });
  },

  // feedServer: serves whatever `feedServer.body` holds; set it before subscribing.
  feedServer: async ({ feedItems }, use) => {
    const state = { body: rss('Go Blog', items(feedItems)) };
    const srv = http.createServer((_, res) => {
      res.setHeader('Content-Type', 'application/rss+xml');
      res.end(state.body);
    });
    await new Promise((r) => srv.listen(0, '127.0.0.1', r));
    state.url = `http://127.0.0.1:${srv.address().port}/feed.xml`;
    await use(state);
    srv.close();
  },

  // signedIn: a page of a logged-in browser, subscribed to feedServer.
  signedIn: async ({ page, app, feedServer }, use) => {
    await page.goto(`${app.url}/install`);
    await page.fill('input[name=login]', LOGIN);
    await page.fill('input[name=password]', PASSWORD);
    await page.fill('input[name=password_confirm]', PASSWORD);
    await page.click('button[type=submit]');
    await page.goto(`${app.url}/add`);
    await page.fill('input[name=url]', feedServer.url);
    await page.click('main button[type=submit]'); // not the logout button of the menu
    await page.goto(app.url);
    await use(page);
  },
});

// setConfig changes settings through the configuration page: fields maps a
// field name to a number or text, or to a boolean for a checkbox.
async function setConfig(page, app, fields) {
  await page.goto(`${app.url}/config`);
  for (const [name, value] of Object.entries(fields)) {
    const field = page.locator(`main [name=${name}]`);
    if (typeof value === 'boolean') await field.setChecked(value);
    else await field.fill(String(value));
  }
  await page.click('main button[type=submit]');
  await page.goto(app.url);
}

module.exports = { test, expect, rss, items, setConfig };
