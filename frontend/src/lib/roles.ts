import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';

import { getAccessCatalog, roleRef } from '@/api/catalog';

export type RoleLabel = { module: string; label: string };

function humanize(s: string): string {
  const t = s.replace(/[-_]+/g, ' ').trim();
  return t.charAt(0).toUpperCase() + t.slice(1);
}

/** "stash:inventory_manager" -> { module: "Stash", label: "Inventory manager" } when the catalog doesn't know it. */
export function fallbackRoleLabel(ref: string): RoleLabel {
  const [mod, key] = ref.includes(':') ? ref.split(':', 2) : ['', ref];
  return { module: humanize(mod), label: humanize(key) };
}

/** Friendly module/role names from the live access catalog. */
export function useRoleLabels() {
  const catalog = useQuery({ queryKey: ['access-catalog'], queryFn: getAccessCatalog, staleTime: 60_000 });
  return useMemo(() => {
    const m = new Map<string, RoleLabel>();
    for (const mod of catalog.data?.modules ?? [])
      for (const r of mod.roles) m.set(roleRef(mod.module, r.key), { module: mod.display_name, label: r.label });
    const get = (ref: string) => m.get(ref) ?? fallbackRoleLabel(ref);
    return {
      get,
      /** "Stash: Inventory manager" */
      text: (ref: string) => {
        const l = get(ref);
        return l.module ? `${l.module}: ${l.label}` : l.label;
      },
      /** "Stash: Inventory manager, Viewer · Den: Member" */
      grouped: (refs: string[]) => {
        const by = new Map<string, string[]>();
        for (const ref of refs) {
          const l = get(ref);
          by.set(l.module, [...(by.get(l.module) ?? []), l.label]);
        }
        return [...by].map(([mod, labels]) => (mod ? `${mod}: ${labels.join(', ')}` : labels.join(', '))).join(' · ');
      },
    };
  }, [catalog.data]);
}
