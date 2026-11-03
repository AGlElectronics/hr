import { Link } from '@tanstack/react-router';
import { Check } from 'lucide-react';

import { cn } from '@/lib/utils';

const STEPS = [
  { key: 'details', label: 'Volunteer & department' },
  { key: 'access', label: 'Access' },
  { key: 'review', label: 'Review & complete' },
] as const;

export type OnboardingStep = (typeof STEPS)[number]['key'];

/** Wizard header. Earlier steps link back while the onboarding is editable; a
 *  locked (completed) onboarding shows every step as done. */
export function OnboardingSteps({ current, id, locked }: { current: OnboardingStep; id?: string; locked?: boolean }) {
  const idx = locked ? STEPS.length : STEPS.findIndex((s) => s.key === current);
  return (
    <ol className="flex flex-wrap items-center gap-2 text-sm">
      {STEPS.map((s, i) => {
        const done = i < idx;
        const active = i === idx || (!!locked && s.key === current);
        const badge = (
          <span
            className={cn(
              'inline-flex h-6 w-6 items-center justify-center rounded-full text-xs font-bold',
              done ? 'bg-success text-white' : active ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground',
            )}
          >
            {done ? <Check className="h-3.5 w-3.5" /> : i + 1}
          </span>
        );
        const label = <span className={cn(active ? 'font-semibold' : 'text-muted-foreground')}>{s.label}</span>;
        const canLink = id && !locked && i < idx;
        return (
          <li key={s.key} className="flex items-center gap-2">
            {canLink ? (
              <Link to={`/onboarding/$id/${s.key}`} params={{ id }} className="flex items-center gap-2 hover:underline">
                {badge}
                {label}
              </Link>
            ) : (
              <span className="flex items-center gap-2">
                {badge}
                {label}
              </span>
            )}
            {i < STEPS.length - 1 && <span className="mx-1 h-px w-8 bg-border" />}
          </li>
        );
      })}
    </ol>
  );
}
