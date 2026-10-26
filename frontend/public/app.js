// Progressive enhancement only: confirmation prompts and dismissible flashes.

document.addEventListener('submit', (event) => {
  const form = event.target instanceof Element ? event.target.closest('form[data-confirm]') : null;
  if (!form) return;
  const message = form.getAttribute('data-confirm');
  if (message && !window.confirm(message)) {
    event.preventDefault();
  }
});

window.setTimeout(() => {
  document.querySelectorAll('[data-flash]').forEach((element) => {
    element.classList.add('is-hidden');
    window.setTimeout(() => element.remove(), 300);
  });
}, 7000);
