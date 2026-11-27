// SPDX-License-Identifier: GPL-3.0-or-later
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { actionFor, firstIndex, move, swipeAction } from '../../src/internal/web/static/nav.js';

test('the current item is the opened one, else the first, else none', () => {
  assert.equal(firstIndex(2, 5), 2);
  assert.equal(firstIndex(-1, 5), 0);
  assert.equal(firstIndex(-1, 0), -1);
});

test('moving inside the page gives the neighbour', () => {
  assert.deepEqual(move(1, 5, 1), { index: 2 });
  assert.deepEqual(move(1, 5, -1), { index: 0 });
});

test('moving past an end of the page reports the edge', () => {
  assert.deepEqual(move(4, 5, 1), { edge: 'next' });
  assert.deepEqual(move(0, 5, -1), { edge: 'previous' });
});

test('with no item there is nowhere to go', () => {
  assert.deepEqual(move(-1, 0, 1), {});
  assert.deepEqual(move(-1, 0, -1), {});
});

const press = (k, extra = {}) => actionFor({ key: k, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, ...extra });

test('keys map to actions', () => {
  const expected = {
    n: 'next', ArrowRight: 'next', p: 'previous', ArrowLeft: 'previous', j: 'next-open', k: 'previous-open',
    ' ': 'toggle-open', t: 'toggle-open', m: 'toggle-read', o: 'open-link', '*': 'toggle-star',
    a: 'mark-all-read', z: 'open-unread',
    h: 'home', v: 'view', f: 'feeds', e: 'edit', u: 'update', r: 'reload', '?': 'help', F1: 'help',
  };
  for (const [key, action] of Object.entries(expected)) assert.equal(press(key), action, key);
  assert.equal(press('x'), null);
  assert.equal(press('Enter'), null);
});

test('shift changes only n, p, m and o', () => {
  assert.equal(press('N', { shiftKey: true }), 'next-page');
  assert.equal(press('P', { shiftKey: true }), 'previous-page');
  assert.equal(press('M', { shiftKey: true }), 'toggle-read-open');
  assert.equal(press('O', { shiftKey: true }), 'open-link-here');
  for (const key of ['J', 'K', 'T', 'A', 'Z', 'H', 'V', 'F', 'E', 'U', 'R']) {
    assert.equal(press(key, { shiftKey: true }), null, `shift+${key}`);
  }
});

test('symbols that need shift on some keyboards work with and without it', () => {
  assert.equal(press('?', { shiftKey: true }), 'help');
  assert.equal(press('*', { shiftKey: true }), 'toggle-star');
  assert.equal(press('F1', { shiftKey: true }), 'help');
});

test('caps lock does not change the meaning of a letter', () => {
  assert.equal(press('N'), 'next');
  assert.equal(press('M'), 'toggle-read');
});

test('keys combined with ctrl, alt or meta belong to the browser', () => {
  for (const mod of ['ctrlKey', 'altKey', 'metaKey']) {
    assert.equal(actionFor({ key: 'n', shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, [mod]: true }), null, mod);
  }
});

const touch = (t, x, y) => ({ time: t, x, y });

test('a quick horizontal swipe to the left goes to the next item, to the right to the previous one', () => {
  assert.equal(swipeAction(touch(0, 200, 300), touch(200, 100, 310)), 'next');
  assert.equal(swipeAction(touch(0, 100, 300), touch(200, 200, 290)), 'previous');
});

test('a gesture that is too short, too slow or too vertical is not a swipe', () => {
  assert.equal(swipeAction(touch(0, 200, 300), touch(200, 171, 300)), null, '29 px');
  assert.equal(swipeAction(touch(0, 200, 300), touch(200, 170, 300)), null, 'exactly 30 px');
  assert.equal(swipeAction(touch(0, 200, 300), touch(499, 100, 300)), 'next', '499 ms');
  assert.equal(swipeAction(touch(0, 200, 300), touch(500, 100, 300)), null, 'exactly 500 ms');
  assert.equal(swipeAction(touch(0, 200, 300), touch(200, 100, 374)), 'next', '74 px vertical');
  assert.equal(swipeAction(touch(0, 200, 300), touch(200, 100, 375)), null, 'exactly 75 px vertical');
  assert.equal(swipeAction(touch(0, 200, 300), touch(200, 100, 225)), null, 'upwards, 75 px');
});
