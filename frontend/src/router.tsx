import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  redirect,
  type ErrorComponentProps,
} from '@tanstack/react-router';

import { PERSON_STATUSES, type PersonStatus } from '@/api/people';
import { Layout } from '@/components/Layout';
import { getUserManager, isOidcAuthMode } from '@/auth/oidc';
import { DashboardPage } from '@/pages/DashboardPage';
import { PeoplePage } from '@/pages/PeoplePage';
import { PersonDetailPage } from '@/pages/PersonDetailPage';
import { DepartmentsPage } from '@/pages/DepartmentsPage';
import { OnboardingListPage } from '@/pages/onboarding/OnboardingListPage';
import { OnboardingDetailsPage, type OnboardingPrefill } from '@/pages/onboarding/OnboardingDetailsPage';
import { KeycloakPage } from '@/pages/KeycloakPage';
import { SettingsPage } from '@/pages/SettingsPage';
import { OnboardingAccessPage } from '@/pages/onboarding/OnboardingAccessPage';
import { OnboardingReviewPage } from '@/pages/onboarding/OnboardingReviewPage';
import { LoginPage } from '@/pages/LoginPage';
import { OidcCallbackPage } from '@/pages/OidcCallbackPage';

const rootRoute = createRootRoute({
  component: () => <Outlet />,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  validateSearch: (search: Record<string, unknown>): { redirect: string } => {
    const r = search.redirect;
    if (typeof r === 'string' && r.startsWith('/')) {
      return { redirect: r };
    }
    return { redirect: '/dashboard' };
  },
  component: LoginPage,
});

const oidcCallbackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/auth/callback',
  component: OidcCallbackPage,
});

async function requireSession() {
  if (!isOidcAuthMode()) {
    return;
  }
  const mgr = getUserManager();
  if (!mgr) {
    throw redirect({ to: '/login', search: { redirect: '/dashboard' } });
  }
  let user = await mgr.getUser();
  if (user?.expired) {
    try {
      user = await mgr.signinSilent();
    } catch {
      user = null;
    }
  }
  if (!user) {
    const path =
      typeof window !== 'undefined'
        ? `${window.location.pathname}${window.location.search}`
        : '/dashboard';
    const redirectTo =
      path.startsWith('/login') || path.startsWith('/auth/') ? '/dashboard' : path;
    throw redirect({ to: '/login', search: { redirect: redirectTo } });
  }
}

const appLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  component: Layout,
  beforeLoad: requireSession,
});

const indexRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/dashboard' });
  },
});

const dashboardRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/dashboard',
  component: DashboardPage,
});

const peopleRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/people',
  validateSearch: (search: Record<string, unknown>): { status?: PersonStatus } =>
    PERSON_STATUSES.some((s) => s.value === search.status) ? { status: search.status as PersonStatus } : {},
  component: PeoplePage,
});

const personDetailRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/people/$id',
  validateSearch: (search: Record<string, unknown>): { edit?: boolean } =>
    search.edit === true || search.edit === 'true' || search.edit === '1' ? { edit: true } : {},
  component: PersonDetailPage,
});

const keycloakRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/keycloak',
  component: KeycloakPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/settings',
  component: SettingsPage,
});

const onboardingListRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/onboarding',
  component: OnboardingListPage,
});

const onboardingNewRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/onboarding/new',
  validateSearch: (search: Record<string, unknown>): OnboardingPrefill => {
    const out: OnboardingPrefill = {};
    for (const k of ['person', 'department', 'first_name', 'last_name', 'email'] as const) {
      if (typeof search[k] === 'string' && search[k]) out[k] = search[k] as string;
    }
    return out;
  },
  component: OnboardingDetailsPage,
});

const onboardingDetailsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/onboarding/$id/details',
  component: OnboardingDetailsPage,
});

const onboardingAccessRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/onboarding/$id/access',
  component: OnboardingAccessPage,
});

const onboardingReviewRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/onboarding/$id/review',
  component: OnboardingReviewPage,
});

const departmentsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/departments',
  component: DepartmentsPage,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  oidcCallbackRoute,
  appLayoutRoute.addChildren([
    indexRoute,
    dashboardRoute,
    peopleRoute,
    personDetailRoute,
    onboardingListRoute,
    onboardingNewRoute,
    onboardingDetailsRoute,
    onboardingAccessRoute,
    onboardingReviewRoute,
    departmentsRoute,
    keycloakRoute,
    settingsRoute,
  ]),
]);

function RouteError({ error, reset }: ErrorComponentProps) {
  return (
    <div className="p-6 space-y-3">
      <h1 className="text-xl font-bold">Something went wrong on this page</h1>
      <p className="text-sm text-destructive">{error instanceof Error ? error.message : String(error)}</p>
      <div className="flex gap-2">
        <button type="button" onClick={reset} className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
          Try again
        </button>
        <a href="/dashboard" className="rounded-pill border border-border px-4 py-2 text-sm">Back to dashboard</a>
      </div>
    </div>
  );
}

export const router = createRouter({ routeTree, defaultErrorComponent: RouteError });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
