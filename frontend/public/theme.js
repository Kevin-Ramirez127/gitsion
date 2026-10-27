// Theme preference: dark by default, persisted in a cookie so SSR can render
// the correct palette before the page is painted.
(() => {
  const root = document.documentElement;
  const button = document.querySelector('[data-theme-toggle]');
  const label = button?.querySelector('[data-theme-label]');
  const meta = document.querySelector('meta[name="theme-color"]');
  const cookieName = 'gitsion_theme';

  function apply(theme) {
    const next = theme === 'light' ? 'light' : 'dark';
    root.dataset.theme = next;
    root.style.colorScheme = next;

    if (meta) {
      meta.setAttribute('content', next === 'dark' ? '#0b0b0b' : '#f2f0ea');
    }
    if (label) {
      label.textContent = next === 'dark' ? 'Dark' : 'Light';
    }
    if (button) {
      const other = next === 'dark' ? 'light' : 'dark';
      button.setAttribute('aria-label', `Switch to ${other} mode`);
      button.setAttribute('title', `Switch to ${other} mode`);
    }

    const secure = window.location.protocol === 'https:' ? '; Secure' : '';
    document.cookie = `${cookieName}=${next}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`;
  }

  apply(root.dataset.theme);

  button?.addEventListener('click', () => {
    apply(root.dataset.theme === 'dark' ? 'light' : 'dark');
  });
})();
