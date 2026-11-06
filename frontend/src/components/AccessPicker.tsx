import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  AlertTriangle,
  Check,
  ChevronDown,
  KeyRound,
  Lock,
  RefreshCw,
} from 'lucide-react';

import { getAccessCatalog, roleRef, type CatalogModule, type CatalogRole } from '@/api/catalog';
import type { RoleRef } from '@/api/departments';
import { useDebugMode } from '@/lib/debug';
import { cn } from '@/lib/utils';

type Selection = Record<string, Set<string>>; // module -> role keys

function selectionFromPlan(plan: { role_name: string }[] | undefined): Selection {
  const sel: Selection = {};
  for (const r of plan ?? []) {
    const [mod, key] = r.role_name.includes(':') ? r.role_name.split(':', 2) : ['legacy', r.role_name];
    (sel[mod] ??= new Set()).add(key);
  }
  return sel;
}

export function AccessPicker({
  plan,
  editable,
  onSave,
  saveLabel = 'Save access plan',
  allowSaveWhenClean = false,
  defaults,
  intro,
  subject = 'This person',
}: {
  plan: { role_name: string }[] | undefined;
  editable: boolean;
  onSave: (roles: RoleRef[]) => Promise<unknown>;
  saveLabel?: string;
  /** Wizard pages: the save button also means "continue", so allow it unchanged. */
  allowSaveWhenClean?: boolean;
  /** Role refs that come from the department defaults (shown with a badge). */
  defaults?: string[];
  intro?: ReactNode;
  subject?: string;
}) {
  const catalog = useQuery({ queryKey: ['access-catalog'], queryFn: getAccessCatalog });
  const debug = useDebugMode();
  const [selection, setSelection] = useState<Selection>(() => selectionFromPlan(plan));
  const [enabled, setEnabled] = useState<Set<string>>(
    () => new Set(Object.keys(selectionFromPlan(plan))),
  );
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (dirty) return;
    const s = selectionFromPlan(plan);
    setSelection(s);
    setEnabled(new Set(Object.keys(s)));
  }, [plan, dirty]);

  const modules = catalog.data?.modules ?? [];
  const roleIndex = useMemo(() => {
    const idx = new Map<string, { mod: CatalogModule; role: CatalogRole }>();
    for (const mod of modules) for (const role of mod.roles) idx.set(roleRef(mod.module, role.key), { mod, role });
    return idx;
  }, [modules]);

  const picked = Object.entries(selection).flatMap(([mod, keys]) =>
    enabled.has(mod) ? [...keys].map((k) => roleRef(mod, k)) : [],
  );

  const save = useMutation({
    mutationFn: () =>
      onSave(
        picked.map((ref) => {
          const hit = roleIndex.get(ref);
          return {
            role_name: ref,
            group_name: hit?.role.group || undefined,
            scope: hit?.role.scope || undefined,
          };
        }),
      ),
    onSuccess: () => setDirty(false),
  });
  const defaultSet = new Set(defaults ?? []);

  const toggleModule = (mod: string) => {
    setDirty(true);
    setEnabled((prev) => {
      const next = new Set(prev);
      if (next.has(mod)) next.delete(mod);
      else next.add(mod);
      return next;
    });
  };

  const toggleRole = (mod: string, key: string) => {
    setDirty(true);
    setSelection((prev) => {
      const next: Selection = { ...prev, [mod]: new Set(prev[mod] ?? []) };
      if (next[mod].has(key)) next[mod].delete(key);
      else next[mod].add(key);
      return next;
    });
  };

  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex items-start justify-between gap-3 border-b border-border p-4">
        <div>
          <h2 className="font-semibold flex items-center gap-2">
            <KeyRound className="w-4 h-4 text-primary" /> Access
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {intro ?? <>Switch on the modules {subject.toLowerCase()} needs, then tick what they should be able to do.</>}
          </p>
        </div>
        <button
          type="button"
          onClick={() => void catalog.refetch()}
          className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
        >
          <RefreshCw className={cn('w-3.5 h-3.5', catalog.isFetching && 'animate-spin')} /> Refresh
        </button>
      </header>

      {catalog.isLoading && <p className="p-4 text-sm text-muted-foreground">Loading access options…</p>}
      {catalog.error && (
        <p className="p-4 text-sm text-destructive">{(catalog.error as Error).message}</p>
      )}

      <div className="divide-y divide-border">
        {modules.map((mod) => {
          const on = enabled.has(mod.module);
          const count = on ? selection[mod.module]?.size ?? 0 : 0;
          return (
            <div key={mod.module}>
              <button
                type="button"
                disabled={!editable || mod.status !== 'ok'}
                onClick={() => toggleModule(mod.module)}
                className={cn(
                  'flex w-full items-center gap-3 px-4 py-3 text-left transition-colors',
                  on ? 'bg-primary-soft/50' : 'hover:bg-muted',
                  (!editable || mod.status !== 'ok') && 'cursor-default',
                )}
              >
                <span
                  className={cn(
                    'inline-flex h-5 w-9 shrink-0 items-center rounded-full p-0.5 transition-colors',
                    on ? 'bg-primary' : 'bg-border',
                  )}
                >
                  <span
                    className={cn(
                      'h-4 w-4 rounded-full bg-white shadow transition-transform',
                      on && 'translate-x-4',
                    )}
                  />
                </span>
                <span className="flex-1">
                  <span className="block font-semibold">
                    Needs access to {mod.display_name}
                  </span>
                  <span className="block text-xs text-muted-foreground">
                    {mod.status === 'ok'
                      ? mod.description || mod.display_name
                      : `Can't reach ${mod.display_name} right now${debug && mod.error ? `: ${mod.error}` : ''}`}
                    {debug && <span className="ml-1 font-mono">· {mod.module} · {mod.source}</span>}
                  </span>
                </span>
                {mod.status !== 'ok' ? (
                  <AlertTriangle className="w-4 h-4 text-destructive" />
                ) : count > 0 ? (
                  <span className="rounded-pill bg-primary px-2 py-0.5 text-xs font-semibold text-primary-foreground">
                    {count} selected
                  </span>
                ) : null}
                <ChevronDown
                  className={cn('w-4 h-4 text-muted-foreground transition-transform', on && 'rotate-180')}
                />
              </button>

              {on && mod.status === 'ok' && (
                <div className="grid gap-2 px-4 pb-4 pt-1 sm:grid-cols-2">
                  {mod.roles.map((role) => (
                    <RoleCard
                      key={`${role.key}-${role.group}-${role.scope}`}
                      role={role}
                      selected={selection[mod.module]?.has(role.key) ?? false}
                      fromDefault={defaultSet.has(roleRef(mod.module, role.key))}
                      debug={debug}
                      disabled={!editable || !role.assignable}
                      onToggle={() => toggleRole(mod.module, role.key)}
                    />
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>

      <footer className="border-t border-border p-4 space-y-3">
        <div className="text-sm">
          <span className="font-medium">{subject} will get:</span>{' '}
          {picked.length === 0 ? (
            <span className="text-muted-foreground">nothing yet</span>
          ) : (
            <span className="inline-flex flex-wrap gap-1.5 align-middle">
              {picked.map((ref) => {
                const hit = roleIndex.get(ref);
                return (
                  <span
                    key={ref}
                    className="rounded-pill border border-border bg-muted px-2 py-0.5 text-xs"
                    title={debug && hit?.role.group ? `group ${hit.role.group}` : undefined}
                  >
                    <strong>{hit?.mod.display_name ?? ref.split(':')[0]}</strong> ·{' '}
                    {hit?.role.label ?? ref.split(':')[1]}
                  </span>
                );
              })}
            </span>
          )}
        </div>
        {editable && (
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => save.mutate()}
              disabled={save.isPending || (!dirty && !allowSaveWhenClean)}
              className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60"
            >
              {save.isPending ? 'Saving…' : dirty || allowSaveWhenClean ? saveLabel : 'Saved'}
            </button>
            {save.error && (
              <span className="text-sm text-destructive">{(save.error as Error).message}</span>
            )}
          </div>
        )}
      </footer>
    </section>
  );
}

function RoleCard({
  role,
  selected,
  fromDefault,
  debug,
  disabled,
  onToggle,
}: {
  role: CatalogRole;
  selected: boolean;
  fromDefault: boolean;
  debug: boolean;
  disabled: boolean;
  onToggle: () => void;
}) {
  const [showPerms, setShowPerms] = useState(false);
  return (
    <div
      className={cn(
        'rounded-lg border p-3 transition-colors',
        selected ? 'border-primary bg-primary-soft/40' : 'border-border bg-background',
        !role.assignable && 'opacity-70',
      )}
    >
      <button
        type="button"
        disabled={disabled}
        onClick={onToggle}
        className="flex w-full items-start gap-2 text-left disabled:cursor-not-allowed"
      >
        <span
          className={cn(
            'mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border',
            selected ? 'border-primary bg-primary text-primary-foreground' : 'border-border',
          )}
        >
          {selected ? <Check className="w-3 h-3" /> : !role.assignable ? <Lock className="w-2.5 h-2.5" /> : null}
        </span>
        <span className="flex-1">
          <span className="block text-sm font-semibold">{role.label}</span>
          {role.description && (
            <span className="block text-xs text-muted-foreground">{role.description}</span>
          )}
        </span>
      </button>
      {(role.default || fromDefault || debug) && (
      <div className="mt-2 flex flex-wrap items-center gap-1.5 pl-6 text-[11px]">
        {role.default && (
          <span className="rounded-pill bg-primary-soft px-1.5 py-0.5 font-semibold text-primary">Default</span>
        )}
        {fromDefault && !role.default && (
          <span className="rounded-pill bg-warning-soft px-1.5 py-0.5 font-semibold text-warning-foreground">
            Department default
          </span>
        )}
        {role.default && <span className="text-muted-foreground">Everyone gets this unless you untick it.</span>}
        {debug && role.group && <span className="rounded-pill bg-muted px-1.5 py-0.5 font-mono">{role.group}</span>}
        {debug && <span className="rounded-pill bg-muted px-1.5 py-0.5">{role.scope}</span>}
        {debug && role.permissions.length > 0 && (
          <button
            type="button"
            onClick={() => setShowPerms((v) => !v)}
            className="text-primary hover:underline"
          >
            {showPerms
              ? 'hide'
              : `${role.permissions.length} permission${role.permissions.length === 1 ? '' : 's'}`}
          </button>
        )}
      </div>
      )}
      {role.note && <p className="mt-1 pl-6 text-[11px] text-muted-foreground">{role.note}</p>}
      {debug && showPerms && (
        <div className="mt-2 flex flex-wrap gap-1 pl-6">
          {role.permissions.map((p) => (
            <span key={p} className="rounded bg-muted px-1 py-0.5 font-mono text-[10px]">
              {p}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
