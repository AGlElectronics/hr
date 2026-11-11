import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { AlertTriangle, Check, ChevronDown, KeyRound, Layers, Lock, RefreshCw, ShieldAlert } from 'lucide-react';

import { listBundles, type AccessBundle } from '@/api/admin';
import { getAccessCatalog, isAdminRole, roleRef, type CatalogModule, type CatalogRole } from '@/api/catalog';
import { canGlobal, getMe } from '@/api/me';
import { useDebugMode } from '@/lib/debug';
import { cn } from '@/lib/utils';

type Selection = Record<string, Set<string>>; // module -> role keys

/** A role already on the plan: picked individually (no bundle_id) or from an access group. */
export type PlanRole = { role_name: string; bundle_id?: string; retired?: boolean };

export type PickedRole = { role_name: string; scope?: string };

function selectionFromPlan(plan: PlanRole[] | undefined): Selection {
  const sel: Selection = {};
  for (const r of plan ?? []) {
    if (r.bundle_id) continue;
    const [mod, key] = r.role_name.includes(':') ? r.role_name.split(':', 2) : ['legacy', r.role_name];
    (sel[mod] ??= new Set()).add(key);
  }
  return sel;
}

/**
 * Picks the access of an onboarding or membership: access groups (bundles of
 * module roles HR admins define) plus individual roles from each module's
 * catalog. The server checks every role again; admin roles need the
 * "assign admin roles" permission.
 */
export function AccessPicker({
  plan,
  bundleIds,
  editable,
  onSave,
  saveLabel = 'Save access plan',
  allowSaveWhenClean = false,
  defaults,
  intro,
  subject = 'This person',
}: {
  plan: PlanRole[] | undefined;
  /** Access groups currently assigned. */
  bundleIds?: string[];
  editable: boolean;
  onSave: (roles: PickedRole[], bundleIds: string[]) => Promise<unknown>;
  saveLabel?: string;
  /** Wizard pages: the save button also means "continue", so allow it unchanged. */
  allowSaveWhenClean?: boolean;
  /** Role refs that come from the department defaults (shown with a badge). */
  defaults?: string[];
  intro?: ReactNode;
  subject?: string;
}) {
  const catalog = useQuery({ queryKey: ['access-catalog'], queryFn: getAccessCatalog });
  const bundlesQuery = useQuery({ queryKey: ['access-bundles'], queryFn: listBundles });
  const me = useQuery({ queryKey: ['me'], queryFn: getMe });
  const mayAssignAdmin = canGlobal(me.data, 'access.assign_privileged');
  const debug = useDebugMode();
  const [selection, setSelection] = useState<Selection>(() => selectionFromPlan(plan));
  const [enabled, setEnabled] = useState<Set<string>>(() => new Set(Object.keys(selectionFromPlan(plan))));
  const [bundles, setBundles] = useState<Set<string>>(() => new Set(bundleIds ?? []));
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (dirty) return;
    const s = selectionFromPlan(plan);
    setSelection(s);
    setEnabled(new Set(Object.keys(s)));
    setBundles(new Set(bundleIds ?? []));
  }, [plan, bundleIds, dirty]);

  const modules = useMemo(() => catalog.data?.modules ?? [], [catalog.data]);
  const allBundles = useMemo(() => bundlesQuery.data?.items ?? [], [bundlesQuery.data]);
  const roleIndex = useMemo(() => {
    const idx = new Map<string, { mod: CatalogModule; role: CatalogRole }>();
    for (const mod of modules) for (const role of mod.roles) idx.set(roleRef(mod.module, role.key), { mod, role });
    return idx;
  }, [modules]);
  /** role ref -> name of the selected access group that grants it */
  const fromBundle = useMemo(() => {
    const m = new Map<string, string>();
    for (const b of allBundles) if (bundles.has(b.id)) for (const r of b.roles) if (!m.has(r.role_name)) m.set(r.role_name, b.name);
    return m;
  }, [allBundles, bundles]);
  const retired = (plan ?? []).filter((r) => r.retired && !r.bundle_id).map((r) => r.role_name);

  const picked = Object.entries(selection).flatMap(([mod, keys]) =>
    enabled.has(mod) ? [...keys].map((k) => roleRef(mod, k)) : [],
  );

  const save = useMutation({
    mutationFn: () =>
      onSave(
        picked.map((ref) => ({ role_name: ref, scope: roleIndex.get(ref)?.role.scope || undefined })),
        [...bundles],
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
  const toggleRetired = (ref: string) => {
    const [mod, key] = ref.split(':', 2);
    setEnabled((prev) => new Set(prev).add(mod));
    toggleRole(mod, key);
  };
  const toggleBundle = (id: string) => {
    setDirty(true);
    setBundles((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
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
            {intro ?? <>Pick access groups, then switch on extra modules {subject.toLowerCase()} needs.</>}
          </p>
        </div>
        <button type="button" onClick={() => void catalog.refetch()}
          className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
          <RefreshCw className={cn('w-3.5 h-3.5', catalog.isFetching && 'animate-spin')} /> Refresh
        </button>
      </header>

      {allBundles.length > 0 && (
        <div className="border-b border-border p-4">
          <h3 className="mb-2 flex items-center gap-2 text-sm font-semibold"><Layers className="h-4 w-4 text-primary" /> Access groups</h3>
          <div className="grid gap-2 sm:grid-cols-2">
            {allBundles.map((b) => (
              <BundleCard key={b.id} bundle={b} selected={bundles.has(b.id)} disabled={!editable} onToggle={() => toggleBundle(b.id)}
                label={(ref) => roleIndex.get(ref)?.role.label ?? ref} />
            ))}
          </div>
        </div>
      )}

      {catalog.isLoading && <p className="p-4 text-sm text-muted-foreground">Loading access options…</p>}
      {catalog.error && <p className="p-4 text-sm text-destructive">{(catalog.error as Error).message}</p>}

      {retired.length > 0 && (
        <div className="border-b border-border bg-warning-soft/40 p-4 text-sm">
          <p className="flex items-center gap-2 font-semibold text-warning-foreground">
            <AlertTriangle className="h-4 w-4" /> No longer offered by their module
          </p>
          <p className="mt-1 text-xs text-muted-foreground">These roles are kept until you remove them. Untick to remove.</p>
          <div className="mt-2 flex flex-wrap gap-2">
            {retired.map((ref) => {
              const [mod, key] = ref.split(':', 2);
              const kept = enabled.has(mod) && (selection[mod]?.has(key) ?? false);
              return (
                <label key={ref} className="inline-flex items-center gap-1.5 rounded-pill border border-border bg-background px-2 py-0.5 text-xs">
                  <input type="checkbox" checked={kept} disabled={!editable} onChange={() => toggleRetired(ref)} />
                  <span className="font-mono">{ref}</span>
                  <span className="rounded-pill bg-warning-soft px-1.5 font-semibold text-warning-foreground">retired</span>
                </label>
              );
            })}
          </div>
        </div>
      )}

      <div className="divide-y divide-border">
        {modules.map((mod) => {
          const usable = mod.status !== 'error';
          const on = enabled.has(mod.module);
          const count = on ? selection[mod.module]?.size ?? 0 : 0;
          return (
            <div key={mod.module}>
              <button type="button" disabled={!editable || !usable} onClick={() => toggleModule(mod.module)}
                className={cn('flex w-full items-center gap-3 px-4 py-3 text-left transition-colors',
                  on ? 'bg-primary-soft/50' : 'hover:bg-muted', (!editable || !usable) && 'cursor-default')}>
                <span className={cn('inline-flex h-5 w-9 shrink-0 items-center rounded-full p-0.5 transition-colors', on ? 'bg-primary' : 'bg-border')}>
                  <span className={cn('h-4 w-4 rounded-full bg-white shadow transition-transform', on && 'translate-x-4')} />
                </span>
                <span className="flex-1">
                  <span className="block font-semibold">Needs access to {mod.display_name}</span>
                  <span className="block text-xs text-muted-foreground">
                    {mod.status === 'ok' && (mod.description || mod.display_name)}
                    {mod.status === 'cached' && `${mod.display_name} is offline; showing the roles it offered last time`}
                    {mod.status === 'error' && `Can't reach ${mod.display_name} right now${debug && mod.error ? `: ${mod.error}` : ''}`}
                    {debug && <span className="ml-1 font-mono">· {mod.module} · {mod.source}</span>}
                  </span>
                </span>
                {mod.status !== 'ok' ? (
                  <AlertTriangle className={cn('w-4 h-4', mod.status === 'error' ? 'text-destructive' : 'text-warning')} />
                ) : count > 0 ? (
                  <span className="rounded-pill bg-primary px-2 py-0.5 text-xs font-semibold text-primary-foreground">{count} selected</span>
                ) : null}
                <ChevronDown className={cn('w-4 h-4 text-muted-foreground transition-transform', on && 'rotate-180')} />
              </button>

              {on && usable && (
                <div className="grid gap-2 px-4 pb-4 pt-1 sm:grid-cols-2">
                  {mod.roles.map((role) => {
                    const ref = roleRef(mod.module, role.key);
                    const admin = isAdminRole(role);
                    const selected = selection[mod.module]?.has(role.key) ?? false;
                    return (
                      <RoleCard key={`${role.key}-${role.group}-${role.scope}`} role={role} selected={selected}
                        fromDefault={defaultSet.has(ref)} bundleName={fromBundle.get(ref)} adminLocked={admin && !mayAssignAdmin && !selected}
                        debug={debug} disabled={!editable || !role.assignable || (admin && !mayAssignAdmin && !selected)}
                        onToggle={() => toggleRole(mod.module, role.key)} />
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </div>

      <footer className="border-t border-border p-4 space-y-3">
        <div className="text-sm">
          <span className="font-medium">{subject} will get:</span>{' '}
          {picked.length === 0 && bundles.size === 0 ? (
            <span className="text-muted-foreground">nothing yet</span>
          ) : (
            <span className="inline-flex flex-wrap gap-1.5 align-middle">
              {allBundles.filter((b) => bundles.has(b.id)).map((b) => (
                <span key={b.id} className="rounded-pill border border-primary/40 bg-primary-soft px-2 py-0.5 text-xs">
                  <Layers className="mr-1 inline h-3 w-3" />{b.name}
                </span>
              ))}
              {picked.map((ref) => {
                const hit = roleIndex.get(ref);
                return (
                  <span key={ref} className="rounded-pill border border-border bg-muted px-2 py-0.5 text-xs"
                    title={debug && hit?.role.group ? `group ${hit.role.group}` : undefined}>
                    <strong>{hit?.mod.display_name ?? ref.split(':')[0]}</strong> · {hit?.role.label ?? ref.split(':')[1]}
                  </span>
                );
              })}
            </span>
          )}
        </div>
        {editable && (
          <div className="flex items-center gap-3">
            <button type="button" onClick={() => save.mutate()} disabled={save.isPending || (!dirty && !allowSaveWhenClean)}
              className="rounded-pill bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground disabled:opacity-60">
              {save.isPending ? 'Saving…' : dirty || allowSaveWhenClean ? saveLabel : 'Saved'}
            </button>
            {save.error && <span role="alert" className="text-sm text-destructive">{(save.error as Error).message}</span>}
          </div>
        )}
      </footer>
    </section>
  );
}

function BundleCard({ bundle, selected, disabled, onToggle, label }: {
  bundle: AccessBundle;
  selected: boolean;
  disabled: boolean;
  onToggle: () => void;
  label: (ref: string) => string;
}) {
  return (
    <button type="button" disabled={disabled} onClick={onToggle} aria-pressed={selected}
      className={cn('flex items-start gap-2 rounded-lg border p-3 text-left transition-colors disabled:cursor-not-allowed',
        selected ? 'border-primary bg-primary-soft/40' : 'border-border bg-background hover:bg-muted')}>
      <span className={cn('mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border',
        selected ? 'border-primary bg-primary text-primary-foreground' : 'border-border')}>
        {selected && <Check className="w-3 h-3" />}
      </span>
      <span className="flex-1">
        <span className="block text-sm font-semibold">{bundle.name}</span>
        {bundle.description && <span className="block text-xs text-muted-foreground">{bundle.description}</span>}
        <span className="mt-1 block text-[11px] text-muted-foreground">{bundle.roles.map((r) => label(r.role_name)).join(' · ')}</span>
      </span>
    </button>
  );
}

function RoleCard({ role, selected, fromDefault, bundleName, adminLocked, debug, disabled, onToggle }: {
  role: CatalogRole;
  selected: boolean;
  fromDefault: boolean;
  bundleName?: string;
  adminLocked: boolean;
  debug: boolean;
  disabled: boolean;
  onToggle: () => void;
}) {
  const [showPerms, setShowPerms] = useState(false);
  const viaBundle = !!bundleName && !selected;
  return (
    <div className={cn('rounded-lg border p-3 transition-colors',
      selected || viaBundle ? 'border-primary bg-primary-soft/40' : 'border-border bg-background',
      (!role.assignable || adminLocked) && 'opacity-70')}>
      <button type="button" disabled={disabled} onClick={onToggle}
        className="flex w-full items-start gap-2 text-left disabled:cursor-not-allowed">
        <span className={cn('mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border',
          selected ? 'border-primary bg-primary text-primary-foreground' : viaBundle ? 'border-primary text-primary' : 'border-border')}>
          {selected || viaBundle ? <Check className="w-3 h-3" /> : !role.assignable || adminLocked ? <Lock className="w-2.5 h-2.5" /> : null}
        </span>
        <span className="flex-1">
          <span className="block text-sm font-semibold">{role.label}</span>
          {role.description && <span className="block text-xs text-muted-foreground">{role.description}</span>}
        </span>
      </button>
      {(role.default || fromDefault || viaBundle || role.privileged || adminLocked || debug) && (
        <div className="mt-2 flex flex-wrap items-center gap-1.5 pl-6 text-[11px]">
          {viaBundle && <span className="rounded-pill bg-primary-soft px-1.5 py-0.5 font-semibold text-primary">From {bundleName}</span>}
          {role.default && <span className="rounded-pill bg-primary-soft px-1.5 py-0.5 font-semibold text-primary">Default</span>}
          {fromDefault && !role.default && (
            <span className="rounded-pill bg-warning-soft px-1.5 py-0.5 font-semibold text-warning-foreground">Department default</span>
          )}
          {(role.privileged || adminLocked) && (
            <span className="inline-flex items-center gap-1 rounded-pill bg-destructive/10 px-1.5 py-0.5 font-semibold text-destructive">
              <ShieldAlert className="h-3 w-3" /> {adminLocked ? 'Admin role: only HR admins can assign it' : 'Admin role'}
            </span>
          )}
          {debug && role.group && <span className="rounded-pill bg-muted px-1.5 py-0.5 font-mono">{role.group}</span>}
          {debug && <span className="rounded-pill bg-muted px-1.5 py-0.5">{role.scope}</span>}
          {debug && role.permissions.length > 0 && (
            <button type="button" onClick={() => setShowPerms((v) => !v)} className="text-primary hover:underline">
              {showPerms ? 'hide' : `${role.permissions.length} permission${role.permissions.length === 1 ? '' : 's'}`}
            </button>
          )}
        </div>
      )}
      {role.note && <p className="mt-1 pl-6 text-[11px] text-muted-foreground">{role.note}</p>}
      {debug && showPerms && (
        <div className="mt-2 flex flex-wrap gap-1 pl-6">
          {role.permissions.map((p) => <span key={p} className="rounded bg-muted px-1 py-0.5 font-mono text-[10px]">{p}</span>)}
        </div>
      )}
    </div>
  );
}
