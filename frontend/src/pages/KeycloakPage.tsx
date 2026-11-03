import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ChevronDown, ExternalLink, RefreshCw } from 'lucide-react';

import { keycloakConsoleUrl, listIdPGroups, type IdPGroup } from '@/api/idp';
import { listPeople } from '@/api/people';
import { PersonQuickPanel } from '@/components/PersonQuickPanel';
import { cn } from '@/lib/utils';

type Filter = 'all' | 'department' | 'access' | 'other';

function kindOf(g: IdPGroup): Exclude<Filter, 'all'> {
  if (g.department) return 'department';
  if (g.default_for?.length || /-(volunteer|lead|admin|member|viewer|editor)$/.test(g.name)) return 'access';
  return 'other';
}

/** Read-only view of Keycloak groups and their members, as HR provisioned them. */
export function KeycloakPage() {
  const groups = useQuery({ queryKey: ['idp-groups'], queryFn: listIdPGroups });
  const people = useQuery({ queryKey: ['people', '', ''], queryFn: () => listPeople() });
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<string | null>(null);

  const personByEmail = useMemo(() => {
    const m = new Map<string, string>();
    for (const p of people.data?.items ?? []) m.set(p.email.toLowerCase(), p.id);
    return m;
  }, [people.data]);

  const all = groups.data?.groups ?? [];
  const term = q.trim().toLowerCase();
  const shown = all.filter((g) => {
    if (filter !== 'all' && kindOf(g) !== filter) return false;
    if (!term) return true;
    return (
      g.name.toLowerCase().includes(term) ||
      g.members.some((m) => `${m.username} ${m.email ?? ''} ${m.first_name ?? ''} ${m.last_name ?? ''}`.toLowerCase().includes(term))
    );
  });
  const toggle = (id: string) =>
    setExpanded((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  return (
    <div className="p-6 space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Keycloak groups</h1>
          <p className="text-sm text-muted-foreground">
            Live, read-only view of the identity provider. HR changes these through onboarding, access changes and offboarding.
          </p>
        </div>
        <div className="flex gap-2">
          <button type="button" onClick={() => void groups.refetch()}
            className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm">
            <RefreshCw className={cn('h-3.5 w-3.5', groups.isFetching && 'animate-spin')} /> Refresh
          </button>
          <a href={keycloakConsoleUrl('groups')} target="_blank" rel="noreferrer"
            className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1.5 text-sm">
            <ExternalLink className="h-3.5 w-3.5" /> Open Keycloak console
          </a>
        </div>
      </div>

      {groups.error && <p className="text-destructive">{(groups.error as Error).message}</p>}
      {groups.data && !groups.data.available && (
        <p className="rounded-md border border-warning/40 bg-warning-soft px-4 py-2 text-sm text-warning-foreground">
          HR is running with the fake identity provider, so there is no Keycloak to show.
        </p>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {(['all', 'department', 'access', 'other'] as const).map((f) => (
          <button key={f} type="button" onClick={() => setFilter(f)}
            className={cn('rounded-pill px-3 py-1 text-sm capitalize',
              filter === f ? 'bg-primary-soft text-primary' : 'text-muted-foreground hover:bg-muted')}>
            {f === 'department' ? 'Departments' : f === 'access' ? 'Module access' : f}
            <span className="ml-1 text-xs">({f === 'all' ? all.length : all.filter((g) => kindOf(g) === f).length})</span>
          </button>
        ))}
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search group or member…"
          className="ml-auto w-64 rounded-md border border-input bg-background px-3 py-1.5 text-sm" />
      </div>

      {groups.isLoading && <p className="text-muted-foreground">Loading groups from Keycloak…</p>}

      <ul className="space-y-2">
        {shown.map((g) => {
          const isOpen = expanded.has(g.id) || (!!term && !g.name.toLowerCase().includes(term));
          return (
            <li key={g.id} className="rounded-lg border border-border bg-card">
              <button type="button" onClick={() => toggle(g.id)}
                className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left">
                <span className="min-w-0">
                  <span className="font-mono text-sm font-medium">{g.path}</span>
                  <span className="ml-2 inline-flex flex-wrap gap-1 align-middle">
                    {g.department && (
                      <span className="rounded-pill bg-primary-soft px-2 py-0.5 text-xs text-primary">Department: {g.department.name}</span>
                    )}
                    {g.default_for?.map((d) => (
                      <span key={d} className="rounded-pill bg-muted px-2 py-0.5 text-xs">default for {d}</span>
                    ))}
                  </span>
                </span>
                <span className="flex shrink-0 items-center gap-2 text-sm text-muted-foreground">
                  {g.members.length} {g.members.length === 1 ? 'member' : 'members'}
                  <ChevronDown className={cn('h-4 w-4 transition-transform', isOpen && 'rotate-180')} />
                </span>
              </button>
              {isOpen && (
                <table className="w-full border-t border-border text-sm">
                  <tbody className="divide-y divide-border">
                    {g.members.map((m) => {
                      const pid = m.email ? personByEmail.get(m.email.toLowerCase()) : undefined;
                      return (
                        <tr key={m.id}>
                          <td className="px-4 py-2">{[m.first_name, m.last_name].filter(Boolean).join(' ') || '—'}</td>
                          <td className="px-4 py-2 font-mono text-xs">{m.username}</td>
                          <td className="px-4 py-2 text-xs">
                            {m.enabled ? <span className="text-success">enabled</span> : <span className="text-destructive">disabled</span>}
                          </td>
                          <td className="px-4 py-2 text-right">
                            {pid ? (
                              <button type="button" onClick={() => setOpen(pid)} className="text-primary hover:underline">HR profile</button>
                            ) : (
                              <span className="text-xs text-muted-foreground">not in HR</span>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                    {g.members.length === 0 && (
                      <tr><td className="px-4 py-2 text-muted-foreground">No members.</td></tr>
                    )}
                  </tbody>
                </table>
              )}
            </li>
          );
        })}
        {!groups.isLoading && shown.length === 0 && groups.data?.available && (
          <li className="text-sm text-muted-foreground">No groups match.</li>
        )}
      </ul>

      {open && <PersonQuickPanel personId={open} onClose={() => setOpen(null)} />}
    </div>
  );
}
