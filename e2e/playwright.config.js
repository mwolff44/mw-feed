// SPDX-License-Identifier: GPL-3.0-or-later
// Browser tests of mw-feed. Run with `npm test` in this directory.
const { defineConfig, devices } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './tests',
  globalSetup: './support/build.js',
  // Each test starts its own server with its own database, so tests are independent.
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  reporter: 'list',
  use: { ...devices['Desktop Chrome'], trace: 'retain-on-failure' },
});
