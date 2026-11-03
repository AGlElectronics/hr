export const THEME_STORAGE_KEY = 'hr-theme';

export type ThemePreference = 'dark' | 'light';

/** Applies theme to `<html>` and persists to localStorage. */
export function applyTheme(theme: ThemePreference): void {
  if (typeof document === 'undefined') {
    return;
  }
  document.documentElement.classList.toggle('dark', theme === 'dark');
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    /* ignore quota / private mode */
  }
}

export function getAppliedTheme(): ThemePreference {
  if (typeof document === 'undefined') {
    return 'light';
  }
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light';
}
