// SPDX-License-Identifier: GPL-3.0-or-later
// Builds the binary under test once, before the tests.
const { execFileSync } = require('node:child_process');
const path = require('node:path');

module.exports = async () => {
  const root = path.resolve(__dirname, '..', '..');
  execFileSync('go', ['build', '-o', path.join(root, 'e2e', '.build', 'mw-feed'), './cmd/mw-feed'], {
    cwd: path.join(root, 'src'),
    env: { ...process.env, CGO_ENABLED: '0' },
    stdio: 'inherit',
  });
};
