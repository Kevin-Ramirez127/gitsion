// Progressive enhancement: update the dashboard in place when filter links are
// selected. Without JavaScript the links remain ordinary server-rendered URLs.

(() => {
  const dashboardSelector = '#dashboard';
  const navSelector = '.filters, .owner-filter, .feed-more, .empty-state, .search-form';
  const announcer = document.getElementById('feed-announcer');

  let controller = null;
  let requestId = 0;

  function dashboard() {
    return document.querySelector(dashboardSelector);
  }

  function isBusy() {
    return dashboard()?.getAttribute('aria-busy') === 'true';
  }

  function setBusy(value) {
    const element = dashboard();
    if (element) element.setAttribute('aria-busy', value ? 'true' : 'false');
  }

  function setAnnouncement(root) {
    if (!announcer) return;
    const summary = root.querySelector('[data-feed-summary]');
    announcer.textContent = summary ? summary.textContent.replace(/\s+/g, ' ').trim() : '';
  }

  function collapsibleState() {
    const state = new Map();
    document.querySelectorAll('details[data-collapsible]').forEach((element) => {
      const key = element.getAttribute('data-collapsible');
      if (key) state.set(key, element.open);
    });
    return state;
  }

  function restoreCollapsibleState(root, state) {
    root.querySelectorAll('details[data-collapsible]').forEach((element) => {
      const key = element.getAttribute('data-collapsible');
      if (key && state.has(key)) element.open = state.get(key);
    });
  }

  async function load(url, { push }) {
    const current = dashboard();
    if (!current) {
      window.location.assign(url.href);
      return;
    }

    const id = ++requestId;
    const scrollY = window.scrollY;
    const openSections = collapsibleState();
    if (controller) controller.abort();
    const requestController = new AbortController();
    controller = requestController;
    const timeout = window.setTimeout(() => requestController.abort(), 12000);
    setBusy(true);

    try {
      const response = await fetch(url.href, {
        headers: { Accept: 'text/html' },
        credentials: 'same-origin',
        signal: requestController.signal,
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);

      const html = await response.text();
      if (id !== requestId) return;

      const parsed = new DOMParser().parseFromString(html, 'text/html');
      const next = parsed.querySelector(dashboardSelector);
      if (!next) throw new Error('Dashboard content missing');

      restoreCollapsibleState(next, openSections);
      current.replaceWith(next);
      if (push) window.history.pushState({ feed: true }, '', url.href);
      window.scrollTo(0, scrollY);
      setBusy(false);
      setAnnouncement(next);

      const heading = next.querySelector('[data-feed-heading]');
      if (heading instanceof HTMLElement) heading.focus({ preventScroll: true });
    } catch (error) {
      if (error && error.name === 'AbortError') {
        if (id === requestId) window.location.assign(url.href);
        return;
      }
      window.location.assign(url.href);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  document.addEventListener('click', (event) => {
    if (event.defaultPrevented || event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;

    const target = event.target instanceof Element ? event.target : null;
    const link = target?.closest('a[href]');
    if (!link || !link.closest(navSelector)) return;
    if (link.target && link.target !== '_self') return;

    const url = new URL(link.getAttribute('href'), window.location.href);
    if (url.origin !== window.location.origin || url.pathname !== '/') return;

    event.preventDefault();
    if (isBusy() || url.href === window.location.href) return;

    load(url, { push: true });
  });

  document.addEventListener('submit', (event) => {
    if (event.defaultPrevented) return;

    const form = event.target instanceof Element ? event.target.closest('form[data-feed-search]') : null;
    if (!form) return;

    event.preventDefault();
    if (isBusy()) return;

    const action = form.getAttribute('action') || window.location.pathname;
    const url = new URL(action, window.location.href);
    const params = new URLSearchParams();

    for (const [key, raw] of new FormData(form).entries()) {
      if (typeof raw !== 'string') continue;
      const value = key === 'q' ? raw.trim() : raw;
      if (value) params.append(key, value);
    }

    url.search = params.toString();
    if (url.href === window.location.href) return;

    load(url, { push: true });
  });

  window.addEventListener('popstate', () => {
    load(new URL(window.location.href), { push: false });
  });
})();
