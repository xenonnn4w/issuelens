const root = document.documentElement;
const preference = window.matchMedia('(prefers-color-scheme: dark)');
let selected: string | null = null;
try { selected = localStorage.getItem('architecture-theme'); } catch {}
const applyTheme = (dark: boolean) => {
  root.dataset.theme = dark ? 'dark' : 'light';
  const toggle = document.querySelector<HTMLInputElement>('#night-mode');
  if (toggle) toggle.checked = dark;
};
applyTheme(selected === 'dark' || (selected !== 'light' && preference.matches));
preference.addEventListener('change', event => {
  if (selected !== 'light' && selected !== 'dark') applyTheme(event.matches);
});
document.addEventListener('DOMContentLoaded', () => {
  const toggle = document.querySelector<HTMLInputElement>('#night-mode');
  if (!toggle) return;
  toggle.checked = root.dataset.theme === 'dark';
  toggle.addEventListener('change', () => {
    selected = toggle.checked ? 'dark' : 'light';
    applyTheme(toggle.checked);
    try { localStorage.setItem('architecture-theme', selected); } catch {}
  });
});
