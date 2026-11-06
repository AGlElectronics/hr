import { Link, Outlet, useRouterState } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import {
  Bug,
  Building2,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Settings,
  ShieldX,
  UserPlus,
  Users,
} from 'lucide-react';

import { getMe } from '@/api/me';
import { isOidcAuthMode, signOutOidc } from '@/auth/oidc';
import { ThemeToggle } from '@/components/ThemeToggle';
import { setDebugMode, useDebugMode } from '@/lib/debug';
import { useModuleBrand } from '@/lib/moduleBrand';
import { useThemeSync } from '@/lib/theme';
import { cn } from '@/lib/utils';

type NavItem = { to: string; label: string; icon: React.ComponentType<{ className?: string }> };

const NAV: NavItem[] = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/onboarding', label: 'Onboarding', icon: UserPlus },
  { to: '/people', label: 'People', icon: Users },
  { to: '/departments', label: 'Departments', icon: Building2 },
  { to: '/settings', label: 'Settings', icon: Settings },
];

/** Technical views for whoever runs the stack; tucked away from day-to-day HR work. */
const DEBUG_NAV: NavItem[] = [{ to: '/keycloak', label: 'Keycloak groups', icon: KeyRound }];

function Wordmark() {
  const brand = useModuleBrand('/api/brand', 'HR');
  return (
    <div className="flex items-center gap-2.5">
      {brand?.logo_url ? (
        <img src={brand.logo_url} alt="" className="h-8 w-8 object-contain" />
      ) : (
        <span className="rounded-pill bg-primary-soft p-1.5">
          <Users className="w-5 h-5 text-primary" />
        </span>
      )}
      <span className="leading-none">
        <span className="block font-sans text-xl font-extrabold tracking-tight">HR</span>
        <span className="block text-[11px] font-medium text-muted-foreground mt-0.5">
          {brand?.organization_name || 'People & access'}
        </span>
      </span>
    </div>
  );
}

function NavLinks({ items }: { items: NavItem[] }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  return (
    <div className="space-y-0.5">
      {items.map((item) => {
        const isActive = pathname === item.to || pathname.startsWith(item.to + '/');
        return (
          <Link
            key={item.to}
            to={item.to}
            className={cn(
              'flex items-center gap-2.5 px-3 py-2 rounded-pill text-sm font-medium whitespace-nowrap transition-colors',
              isActive ? 'bg-primary-soft text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
            )}
          >
            <item.icon className="w-4 h-4 shrink-0" />
            {item.label}
          </Link>
        );
      })}
    </div>
  );
}

function DebugSection() {
  const on = useDebugMode();
  return (
    <div>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        onClick={() => setDebugMode(!on)}
        title="Shows technical details such as group names, role keys and permissions"
        className="flex w-full items-center gap-2.5 rounded-pill px-3 py-2 text-sm font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <Bug className="h-4 w-4 shrink-0" />
        Debug mode
        <span className={cn('ml-auto inline-flex h-4 w-7 shrink-0 items-center rounded-full p-0.5 transition-colors', on ? 'bg-primary' : 'bg-border')}>
          <span className={cn('h-3 w-3 rounded-full bg-white shadow transition-transform', on && 'translate-x-3')} />
        </span>
      </button>
      {on && (
        <div className="ml-3 border-l border-border pl-2">
          <NavLinks items={DEBUG_NAV} />
        </div>
      )}
    </div>
  );
}

function SignOutButton() {
  if (!isOidcAuthMode()) return null;
  return (
    <button type="button" onClick={() => void signOutOidc()}
      className="inline-flex items-center gap-1 text-primary hover:underline">
      <LogOut className="w-3 h-3" /> Sign out
    </button>
  );
}

function NoAccess({ username }: { username: string }) {
  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-6 text-foreground">
      <div className="max-w-md space-y-4 rounded-lg border border-border bg-card p-6 text-center">
        <ShieldX className="mx-auto h-10 w-10 text-destructive" />
        <h1 className="text-xl font-bold">No access to HR</h1>
        <p className="text-sm text-muted-foreground">
          You are signed in as <span className="font-semibold text-foreground">{username}</span>, but your account is not
          allowed to use HR. Ask an HR admin to give you HR access.
        </p>
        <div className="flex justify-center gap-3 text-sm">
          <SignOutButton />
        </div>
      </div>
    </div>
  );
}

export function Layout() {
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  useThemeSync('/api/me/preferences');

  if (me.isLoading) {
    return <div className="min-h-screen bg-background p-6 text-muted-foreground">Loading…</div>;
  }
  if (me.data && !me.data.has_access) {
    return <NoAccess username={me.data.username} />;
  }

  return (
    <div className="min-h-screen w-full flex flex-col md:flex-row bg-background text-foreground">
      <aside className="shrink-0 md:w-60 md:h-screen md:sticky md:top-0 border-b md:border-b-0 md:border-r border-border bg-card flex flex-col">
        <div className="px-4 py-4 border-b border-border">
          <Wordmark />
        </div>
        <nav className="p-2 md:flex-1 overflow-y-auto">
          <NavLinks items={NAV} />
        </nav>
        <div className="p-2 border-t border-border space-y-0.5">
          <DebugSection />
          <ThemeToggle className="w-full" />
        </div>
        <div className="p-3 border-t border-border text-xs text-muted-foreground">
          <div>
            Signed in as <span className="font-semibold text-foreground">{me.data?.username ?? '…'}</span>
          </div>
          <div className="mt-2">
            <SignOutButton />
          </div>
        </div>
      </aside>

      <main className="flex-1 min-w-0 min-h-0 flex flex-col w-full md:min-h-screen">
        {me.error ? (
          <p className="p-6 text-destructive">Could not load your account: {(me.error as Error).message}</p>
        ) : (
          <Outlet />
        )}
      </main>
    </div>
  );
}
