import { useSearch } from '@tanstack/react-router';
import { useState } from 'react';

import { isOidcAuthMode, signInWithOidc } from '@/auth/oidc';
import { LoginBackdrop, loginLook } from '@/components/LoginBackdrop';
import { useModuleBrand } from '@/lib/moduleBrand';

export function LoginPage() {
  const { redirect } = useSearch({ from: '/login' });
  const [error, setError] = useState<string | null>(null);
  const brand = useModuleBrand('/api/brand', 'HR');
  const look = loginLook(brand?.login_background_url, brand?.copy?.login_layout, brand?.copy?.login_caption);

  return (
    <LoginBackdrop look={look} plain="min-h-screen grid place-items-center bg-background">
      <div className="login-card w-full max-w-md rounded-lg border border-border bg-card p-8 shadow-sm">
        <h1 className="text-2xl font-extrabold tracking-tight">Sign in to HR</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {isOidcAuthMode()
            ? 'Use the same account as for the portal and the other modules.'
            : 'Dev mode — the SPA stamps X-Dev-User; no IdP login.'}
        </p>
        {isOidcAuthMode() ? (
          <button
            type="button"
            className="mt-6 w-full rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground"
            onClick={() => {
              setError(null);
              signInWithOidc(redirect).catch((e: unknown) =>
                setError(e instanceof Error ? e.message : 'Sign-in failed'),
              );
            }}
          >
            Sign in
          </button>
        ) : (
          <a
            href={redirect || '/dashboard'}
            className="mt-6 inline-flex w-full items-center justify-center rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground"
          >
            Continue
          </a>
        )}
        {error && (
          <p className="mt-3 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </div>
    </LoginBackdrop>
  );
}
