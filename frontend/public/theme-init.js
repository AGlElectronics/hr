// Apply the cached light/dark choice before first paint (see src/lib/theme.ts).
// A file, not an inline <script>: the Content Security Policy allows only
// scripts from the app itself (script-src 'self').
(function () {
  var p = 'system';
  try {
    p = localStorage.getItem('cs-theme') || localStorage.getItem('hr-theme') || 'system';
  } catch {
    // Storage blocked (private mode): follow the system setting.
  }
  var dark = p === 'dark' || (p !== 'light' && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.classList.toggle('dark', dark);
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
})();
