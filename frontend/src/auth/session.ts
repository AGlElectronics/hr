import type { User } from 'oidc-client-ts';

import { setUnauthorizedHandler } from '@/api/client';
import { getUserManager } from '@/auth/oidc';

/*
 * Keeps every tab on the identity Keycloak currently has. Tokens live in
 * memory per tab, so tabs learn about each other through the Keycloak
 * session (the session monitor), not through storage:
 *  - Keycloak session ends (sign-out in another tab) -> this tab goes to the login page
 *  - another person signs in (new session)            -> silent sign-in, then reload as that person
 *  - the API answers 401                               -> back to the login page
 * Without this, a tab kept a stale admin token after a user switch.
 */

export function goToLogin(): void {
  const path = `${window.location.pathname}${window.location.search}`;
  if (path.startsWith('/login') || path.startsWith('/auth/')) return;
  window.location.assign(`/login?redirect=${encodeURIComponent(path)}`);
}

let installed = false;

export function installSessionSync(): void {
  const mgr = getUserManager();
  if (!mgr || installed || typeof window === 'undefined') return;
  installed = true;

  let currentSub: string | undefined;
  void mgr.getUser().then((u) => {
    currentSub = u?.profile.sub;
  });
  mgr.events.addUserLoaded((u: User) => {
    const next = u.profile.sub;
    if (currentSub !== undefined && next !== currentSub) {
      window.location.reload();
      return;
    }
    currentSub = next;
  });

  const endSession = () => {
    void mgr.removeUser().finally(goToLogin);
  };
  mgr.events.addUserSignedOut(endSession);
  // New Keycloak session (signed out and back in elsewhere): adopt it
  // silently; addUserLoaded reloads when it belongs to someone else.
  mgr.events.addUserSessionChanged(() => {
    void mgr.signinSilent().catch(endSession);
  });
  mgr.events.addSilentRenewError((err) => {
    if (/login_required|invalid_grant|session not active/i.test(String(err?.message ?? err))) endSession();
  });

  let redirecting = false;
  setUnauthorizedHandler(() => {
    if (redirecting) return;
    redirecting = true;
    endSession();
  });
}
