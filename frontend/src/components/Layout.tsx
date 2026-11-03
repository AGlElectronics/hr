import { Link, Outlet, useRouterState } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { Building2, ClipboardCheck, KeyRound, LayoutDashboard, LogOut, Settings, UserPlus, Users } from 'lucide-react';

import { apiRequest } from '@/api/client';
import { isOidcAuthMode, signOutOidc } from '@/auth/oidc';
import { cn } from '@/lib/utils';

type NavItem = { to: string; label: string; icon: React.ComponentType<{ className?: string }> };

const NAV: NavItem[] = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/onboarding', label: 'Onboarding', icon: UserPlus },
  { to: '/people', label: 'People', icon: Users },
  { to: '/departments', label: 'Departments', icon: Building2 },
  { to: '/approvals', label: 'Approvals', icon: ClipboardCheck },
  { to: '/keycloak', label: 'Keycloak', icon: KeyRound },
  { to: '/settings', label: 'Settings', icon: Settings },
];

type Me = { username: string; auth_mode: string };

function Wordmark() {
  return (
    <div className="flex items-center gap-2.5">
      <span className="rounded-pill bg-primary-soft p-1.5">
        <Users className="w-5 h-5 text-primary" />
      </span>
      <span className="leading-none">
        <span className="block font-sans text-xl font-extrabold tracking-tight">HR</span>
        <span className="block text-[11px] font-medium text-muted-foreground mt-0.5">
          Con Services
        </span>
      </span>
    </div>
  );
}

function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  return (
    <div className="space-y-0.5">
      {NAV.map((item) => {
        const isActive = pathname === item.to || pathname.startsWith(item.to + '/');
        return (
          <Link
            key={item.to}
            to={item.to}
            onClick={onNavigate}
            className={cn(
              'flex items-center gap-2.5 px-3 py-2 rounded-pill text-sm font-medium whitespace-nowrap transition-colors',
              isActive
                ? 'bg-primary-soft text-primary'
                : 'text-muted-foreground hover:bg-muted hover:text-foreground',
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

export function Layout() {
  const me = useQuery({
    queryKey: ['me'],
    queryFn: () => apiRequest<Me>('/api/me'),
  });

  return (
    <div className="min-h-screen w-full flex flex-col md:flex-row bg-background text-foreground">
      <aside className="shrink-0 md:w-60 md:min-h-screen border-b md:border-b-0 md:border-r border-border bg-card flex flex-col">
        <div className="px-4 py-4 border-b border-border">
          <Wordmark />
        </div>
        <nav className="p-2 md:flex-1 overflow-y-auto">
          <NavLinks />
        </nav>
        <div className="mt-auto p-3 border-t border-border text-xs text-muted-foreground">
          <div>
            Signed in as{' '}
            <span className="font-semibold text-foreground">
              {me.data?.username ?? '…'}
            </span>
          </div>
          <div className="mt-1 text-[10px] uppercase tracking-wider">
            {me.data?.auth_mode ?? '…'}
          </div>
          {isOidcAuthMode() ? (
            <button
              type="button"
              onClick={() => void signOutOidc()}
              className="mt-2 inline-flex items-center gap-1 text-primary hover:underline"
            >
              <LogOut className="w-3 h-3" /> Sign out
            </button>
          ) : null}
        </div>
      </aside>

      <main className="flex-1 min-w-0 min-h-0 flex flex-col w-full md:min-h-screen">
        <Outlet />
      </main>
    </div>
  );
}
