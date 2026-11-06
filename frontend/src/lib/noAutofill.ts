/**
 * Spread on non-login inputs so browsers and password managers (1Password,
 * LastPass, Bitwarden) leave them alone; their overlays otherwise linger
 * across route changes.
 */
export const noAutofill = {
  autoComplete: 'off',
  'data-1p-ignore': 'true',
  'data-lpignore': 'true',
  'data-bwignore': 'true',
  'data-form-type': 'other',
} as const;
