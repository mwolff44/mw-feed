// SPDX-License-Identifier: GPL-3.0-or-later
// Navigation logic without any DOM, so that it can be tested on its own.

// firstIndex is the item that is current when the page loads: the opened one,
// else the first. openIndex is -1 when no item is opened.
export function firstIndex(openIndex, count) {
  if (openIndex >= 0) return openIndex;
  return count > 0 ? 0 : -1;
}

// move says where to go from index when direction is 1 (next) or -1 (previous):
// { index } inside the page, { edge: 'next' | 'previous' } past an end of the
// page, {} when the page has no item.
export function move(index, count, direction) {
  if (count <= 0) return {};
  const target = index + direction;
  if (target >= count) return { edge: 'next' };
  if (target < 0) return { edge: 'previous' };
  return { index: target };
}

// Letters that mean something else with shift held down.
const shiftable = { n: ['next', 'next-page'], p: ['previous', 'previous-page'], m: ['toggle-read', 'toggle-read-open'], o: ['open-link', 'open-link-here'] };

// Letters (and space) that only count without shift.
const plain = {
  ArrowRight: 'next',
  ArrowLeft: 'previous',
  j: 'next-open',
  k: 'previous-open',
  ' ': 'toggle-open',
  t: 'toggle-open',
  a: 'mark-all-read',
  z: 'open-unread',
  h: 'home',
  v: 'view',
  f: 'feeds',
  e: 'edit',
  u: 'update',
  r: 'reload',
};

// Symbols and function keys: some keyboards need shift to type them.
const symbols = { '*': 'toggle-star', '?': 'help', F1: 'help' };

// actionFor is the action of a key press, or null. Combinations with ctrl, alt
// or meta are left to the browser. The caps lock key does not change a letter's
// meaning: only shift does.
export function actionFor(event) {
  if (event.ctrlKey || event.altKey || event.metaKey) return null;
  if (Object.hasOwn(symbols, event.key)) return symbols[event.key];
  const key = event.key.length === 1 ? event.key.toLowerCase() : event.key;
  if (Object.hasOwn(shiftable, key)) return shiftable[key][event.shiftKey ? 1 : 0];
  if (event.shiftKey) return null;
  return Object.hasOwn(plain, key) ? plain[key] : null;
}

// A gesture is a swipe when it is quick, mostly horizontal and long enough.
const swipeMaxDuration = 500; // ms
const swipeMinDistance = 30; // px, horizontal
const swipeMaxDrift = 75; // px, vertical

// swipeAction is the action of a finger gesture from start to end ({ time, x, y }):
// a swipe to the left goes to the next item, to the right to the previous one.
export function swipeAction(start, end) {
  const dx = start.x - end.x;
  const quick = end.time - start.time < swipeMaxDuration;
  if (!quick || Math.abs(dx) <= swipeMinDistance || Math.abs(start.y - end.y) >= swipeMaxDrift) return null;
  return dx > 0 ? 'next' : 'previous';
}
