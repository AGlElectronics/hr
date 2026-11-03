import { useEffect, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';

import { getUserManager } from '@/auth/oidc';
import { oidcCallbackUrl } from '@/auth/captureCallbackUrl';

export function OidcCallbackPage() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const mgr = getUserManager();
    if (!mgr) {
      setError('OIDC is not configured.');
      return;
    }
    mgr
      .signinRedirectCallback(oidcCallbackUrl())
      .then((user) => {
        const target = (user?.state as string | undefined) ?? '/dashboard';
        void navigate({ to: target.startsWith('/') ? target : '/dashboard' });
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err));
      });
  }, [navigate]);

  return (
    <div className="min-h-screen grid place-items-center bg-background">
      <div className="max-w-md rounded-lg border border-border bg-card p-6 text-center">
        {error ? (
          <>
            <h1 className="text-xl font-bold">Sign-in failed</h1>
            <p className="mt-2 text-sm text-muted-foreground">{error}</p>
          </>
        ) : (
          <>
            <h1 className="text-xl font-bold">Signing you in…</h1>
            <p className="mt-2 text-sm text-muted-foreground">One moment.</p>
          </>
        )}
      </div>
    </div>
  );
}
