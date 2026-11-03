import { useSearch } from '@tanstack/react-router';

import { isOidcAuthMode, signInWithOidc } from '@/auth/oidc';

export function LoginPage() {
  const { redirect } = useSearch({ from: '/login' });

  return (
    <div className="min-h-screen grid place-items-center bg-background">
      <div className="w-full max-w-md rounded-lg border border-border bg-card p-8 shadow-sm">
        <h1 className="text-2xl font-extrabold tracking-tight">Sign in to HR</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {isOidcAuthMode()
            ? 'Use the shared Keycloak (same realm as Den / Stash / Events).'
            : 'Dev mode — the SPA stamps X-Dev-User; no IdP login.'}
        </p>
        {isOidcAuthMode() ? (
          <button
            type="button"
            className="mt-6 w-full rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground"
            onClick={() => void signInWithOidc(redirect)}
          >
            Continue with SSO
          </button>
        ) : (
          <a
            href={redirect || '/dashboard'}
            className="mt-6 inline-flex w-full items-center justify-center rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground"
          >
            Continue
          </a>
        )}
      </div>
    </div>
  );
}
