// SPDX-License-Identifier: GPL-3.0-or-later
// Progressive enhancement: every page and action works without this script.
// When something goes wrong here, the form is sent the ordinary way.

import { actionFor, firstIndex, move, swipeAction } from './nav.js';

document.documentElement.classList.add('js');

const itemAction = /^\/item\/(\d+)\/(?:read|unread|star|unstar)$/;

const parseHTML = (text) => new DOMParser().parseFromString(text, 'text/html');

// post sends a form the way the browser would (urlencoded) and returns the page
// the server redirects to.
async function post(form) {
  const resp = await fetch(form.action, {
    method: 'POST',
    body: new URLSearchParams(new FormData(form)),
    credentials: 'same-origin',
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  return parseHTML(await resp.text());
}

// fragment returns the up-to-date HTML of an item, as the list shows it.
async function fragment(id, back) {
  const resp = await fetch(`/item/${id}?return=${encodeURIComponent(back)}`, { credentials: 'same-origin' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  const el = parseHTML(await resp.text()).getElementById(`item-${id}`);
  if (!el) throw new Error('no item in the fragment');
  return el;
}

// refresh replaces the item and the counters with what the server says now.
async function refreshAfterAction(form, id) {
  const where = form.elements.return ? form.elements.return.value : '/';
  const after = await post(form);
  const fresh = await fragment(id, where);
  const old = document.getElementById(`item-${id}`);
  const nav = after.querySelector('nav.feeds');
  const oldNav = document.querySelector('nav.feeds');
  const count = after.querySelector('.reading h1 small');
  const oldCount = document.querySelector('.reading h1 small');
  if (!old || (oldNav && !nav) || (oldCount && !count)) throw new Error('unexpected page');
  const position = [...old.querySelectorAll('.actions form')].indexOf(form);
  const wasCurrent = old.classList.contains('current');
  if (old.tagName === 'DETAILS') fresh.open = old.open; // the server sends it open; keep what the visitor chose
  old.replaceWith(document.importNode(fresh, true));
  if (wasCurrent) markCurrent(document.getElementById(`item-${id}`));
  if (oldNav) oldNav.replaceWith(document.importNode(nav, true));
  if (oldCount) oldCount.textContent = count.textContent;
  // The button that was used is gone: keep the keyboard where it was.
  const button = document.querySelectorAll(`#item-${id} .actions form`)[position]?.querySelector('button');
  if (button) button.focus();
}

document.addEventListener('submit', (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || form.method !== 'post') return;
  const match = itemAction.exec(new URL(form.action).pathname);
  if (!match) return;
  event.preventDefault();
  const button = form.querySelector('button');
  if (button) button.disabled = true;
  refreshAfterAction(form, match[1]).catch(() => form.submit());
});

// ---- The current item and moving between items ----

const landOnLast = 'mw-feed.land-on-last';

const reading = () => document.querySelector('.reading');
const itemList = () => [...document.querySelectorAll('.reading .item')];
const currentIndex = () => itemList().findIndex((el) => el.classList.contains('current'));

// markCurrent makes el the one current item.
function markCurrent(el) {
  for (const other of document.querySelectorAll('.item.current')) {
    other.classList.remove('current');
    other.removeAttribute('aria-current');
  }
  el.classList.add('current');
  el.setAttribute('aria-current', 'true');
}

// goTo moves to an item: the one left is closed, the new one is opened on request.
function goTo(index, open) {
  const items = itemList();
  const left = items[currentIndex()];
  const target = items[index];
  if (left && left !== target && left.tagName === 'DETAILS') left.open = false;
  markCurrent(target);
  if (open && target.tagName === 'DETAILS') target.open = true;
  // Scroll only when the item is not already visible, and leave the address bar alone.
  if (reading()?.hasAttribute('data-autofocus')) target.scrollIntoView({ block: 'nearest' });
}

// markLeftRead marks the item that was left as read, when it is unread. A
// failure leaves it unread: moving on is not interrupted.
async function markLeftRead(item) {
  const form = item?.classList.contains('unread') && item.querySelector('form[action$="/read"]');
  if (!form) return;
  try {
    await refreshAfterAction(form, item.id.replace('item-', ''));
  } catch {
    // the item stays unread
  }
}

async function step(direction, open) {
  const items = itemList();
  const result = move(currentIndex(), items.length, direction);
  // Only moving forward reads the item that is left.
  const autoread = direction === 1 && reading()?.hasAttribute('data-autoread-item');
  const left = currentItem();
  if ('index' in result) {
    goTo(result.index, open);
    if (autoread) markLeftRead(left);
    return;
  }
  const link = result.edge && document.querySelector(`.pager a[rel=${result.edge === 'next' ? 'next' : 'prev'}]`);
  if (!link) return; // no neighbouring page: nowhere to go
  if (result.edge === 'previous') sessionStorage.setItem(landOnLast, '1');
  // The request must be done before the page changes, or the navigation cuts it.
  if (autoread) await markLeftRead(left);
  location.href = link.href;
}

const editable = (el) => el instanceof HTMLElement && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName));

// markAllRead sends the menu's mark-read form, after the visitor confirms.
function markAllRead() {
  const button = document.querySelector('form[action="/mark"] button[value="1"]');
  if (button && confirm(button.dataset.confirm)) button.form.requestSubmit(button);
}

const maxTabs = 10; // tabs opened by one press of z

// openUnread opens the unread items of the page in tabs, then marks them read.
// It stops at the first tab the browser blocks, so no item is marked read unseen.
async function openUnread() {
  const opened = [];
  for (const item of document.querySelectorAll('.reading .item.unread')) {
    if (opened.length === maxTabs) break;
    const link = item.querySelector('a.item-link');
    const tab = link && window.open(link.href, '_blank');
    if (!tab) break;
    tab.opener = null;
    opened.push(item.id.replace('item-', ''));
  }
  for (const id of opened) {
    const form = document.querySelector(`#item-${id} form[action$="/read"]`);
    if (!form) continue;
    try {
      await refreshAfterAction(form, id);
    } catch {
      return; // the tabs are open; the items stay unread rather than reloading the page under the visitor
    }
  }
}

const currentItem = () => itemList()[currentIndex()];
const submit = (form) => form && form.requestSubmit();
const click = (selector) => document.querySelector(selector)?.click();

function toggleOpen() {
  const item = currentItem();
  if (item?.tagName === 'DETAILS') item.open = !item.open;
}

function openLink(here) {
  const link = currentItem()?.querySelector('a.item-link');
  if (!link) return;
  if (here) {
    location.href = link.href;
    return;
  }
  const tab = window.open(link.href, '_blank');
  if (tab) tab.opener = null;
}

const itemForm = (suffixes) => currentItem()?.querySelector(suffixes.map((s) => `form[action$="${s}"]`).join(','));

// Shortcuts that act on the current item; they need a reading page.
const itemKeys = {
  next: () => step(1, false),
  previous: () => step(-1, false),
  'next-open': () => step(1, true),
  'previous-open': () => step(-1, true),
  'toggle-open': toggleOpen,
  'toggle-read': () => submit(itemForm(['/read', '/unread'])),
  'toggle-read-open': () => { toggleOpen(); submit(itemForm(['/read', '/unread'])); },
  'toggle-star': () => submit(itemForm(['/star', '/unstar'])),
  'open-link': () => openLink(false),
  'open-link-here': () => openLink(true),
  'next-page': () => click('.pager a[rel=next]'),
  'previous-page': () => click('.pager a[rel=prev]'),
  'mark-all-read': markAllRead,
  'open-unread': openUnread,
  view: () => click('form[action="/display"] button[name=view]'),
  feeds: () => click('form[action="/display"] button[name=listFeeds]'),
  update: () => click('form[action="/update"] button'),
};

// Shortcuts of the menu; they work on every page that has it.
const menuKeys = {
  home: () => click('a[data-shortcut=home]'),
  edit: () => click('a[data-shortcut=edit]'),
  help: () => click('a[data-shortcut=help]'),
  reload: () => location.reload(),
};

// Space keeps its meaning on the controls it already operates.
const operable = (el) => el instanceof HTMLElement && el.closest('button, a, summary') !== null;

document.addEventListener('keydown', (event) => {
  if (event.defaultPrevented || editable(event.target)) return;
  const action = actionFor(event);
  if (!action || (event.key === ' ' && operable(event.target))) return;
  const handler = reading() ? itemKeys[action] : null;
  const menuHandler = document.querySelector('header.top') ? menuKeys[action] : null;
  if (!handler && !menuHandler) return;
  event.preventDefault();
  (handler ?? menuHandler)();
});

document.addEventListener('click', (event) => {
  const item = event.target instanceof Element && event.target.closest('.reading .item');
  if (item) markCurrent(item);
});

// A swipe is judged from where and when the finger went down and came up.
let touchStart = null;

document.addEventListener('touchstart', (event) => {
  const swiping = reading()?.hasAttribute('data-swipe') && event.touches.length === 1 && !editable(event.target);
  const touch = event.touches[0];
  touchStart = swiping ? { time: Date.now(), x: touch.pageX, y: touch.pageY } : null;
}, { passive: true });

document.addEventListener('touchend', (event) => {
  const start = touchStart;
  touchStart = null;
  if (!start || event.touches.length !== 0) return;
  const touch = event.changedTouches[0];
  const action = swipeAction(start, { time: Date.now(), x: touch.pageX, y: touch.pageY });
  if (action) step(action === 'next' ? 1 : -1, false);
}, { passive: true });

function startReading() {
  const items = itemList();
  if (items.length === 0) return;
  if (sessionStorage.getItem(landOnLast)) {
    sessionStorage.removeItem(landOnLast);
    markCurrent(items[items.length - 1]);
    return;
  }
  markCurrent(items[firstIndex(items.findIndex((el) => el.open), items.length)]);
}

startReading();
