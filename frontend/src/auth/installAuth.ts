import { setAuthHeadersResolver } from '@/api/client';
import { getUserManager, isOidcAuthMode } from '@/auth/oidc';
import { installSessionSync } from '@/auth/session';

// VITE_DEV_USER only applies to `npm run dev`; production builds always use
// the default name, which only a backend running AUTH_MODE=dev accepts.
const DEV_USER =
  (import.meta.env.DEV ? (import.meta.env.VITE_DEV_USER as string | undefined) : undefined) || 'dev-hr-admin';

export function installAuth(): void {
  if (isOidcAuthMode()) {
    setAuthHeadersResolver(async (): Promise<Record<string, string>> => {
      const mgr = getUserManager();
      if (!mgr) {
        return {};
      }
      const user = await mgr.getUser();
      if (!user) {
        return {};
      }
      const bearer = user.id_token ?? user.access_token;
      if (!bearer || user.expired) {
        return {};
      }
      return { Authorization: `Bearer ${bearer}` };
    });
    installSessionSync();
    return;
  }
  setAuthHeadersResolver(async () => ({ 'X-Dev-User': DEV_USER }));
}
