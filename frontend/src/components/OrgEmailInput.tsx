import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Check, Sparkles } from 'lucide-react';

import { getSettings } from '@/api/settings';
import { renderWithFallback } from '@/lib/naming';
import { noAutofill } from '@/lib/noAutofill';
import { cn } from '@/lib/utils';

/**
 * Organization email: HR types the part before "@"; the domain is fixed by
 * settings and the suggested name comes from the template.
 */
export function OrgEmailInput({
  value,
  onChange,
  firstName,
  lastName,
  nickname,
  invalid,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  firstName: string;
  lastName: string;
  nickname: string;
  invalid?: boolean;
  className: string;
}) {
  const settings = useQuery({ queryKey: ['settings'], queryFn: getSettings, staleTime: 60_000 });
  const domain = settings.data?.org_email_domain ?? '';
  const suffix = domain ? `@${domain}` : '';

  if (!domain) {
    return (
      <div className="space-y-1">
        <input {...noAutofill} className={className} type="email" value={value} aria-invalid={invalid}
          onChange={(e) => onChange(e.target.value)} placeholder="name@your-org.example" />
        {!settings.isLoading && (
          <span className="block text-xs text-muted-foreground">
            Set the organization domain in{' '}
            <Link to="/settings" className="text-primary hover:underline">Settings</Link> to only type the name.
          </span>
        )}
      </div>
    );
  }

  const local = value.toLowerCase().endsWith(suffix) ? value.slice(0, value.length - suffix.length) : value.split('@')[0];
  const suggestion = renderWithFallback(settings.data?.org_email_template, { first: firstName, last: lastName, nickname });
  const setLocal = (raw: string) => {
    const l = raw.split('@')[0].trim();
    onChange(l ? `${l}${suffix}` : '');
  };
  const usingSuggestion = !!suggestion && local === suggestion;

  return (
    <div className="space-y-2">
      <div className={cn(
        'flex items-stretch overflow-hidden rounded-md border border-input bg-background focus-within:ring-2 focus-within:ring-ring',
        invalid && 'border-destructive',
      )}>
        <input
          {...noAutofill}
          className="min-w-0 flex-1 bg-transparent px-3 py-2 text-sm outline-none"
          value={local}
          aria-invalid={invalid}
          aria-label="Organization email name"
          placeholder={suggestion || 'name'}
          onChange={(e) => setLocal(e.target.value)}
          onKeyDown={(e) => {
            if ((e.key === 'Tab' || e.key === 'ArrowRight') && !local && suggestion) {
              e.preventDefault();
              setLocal(suggestion);
            }
          }}
        />
        <span className="flex items-center border-l border-input bg-muted px-3 text-sm text-muted-foreground">{suffix}</span>
      </div>
      {usingSuggestion ? (
        <span className="inline-flex items-center gap-1.5 rounded-pill bg-success-soft px-3 py-1.5 text-sm font-medium text-success-foreground">
          <Check className="h-4 w-4" /> Suggested address used
        </span>
      ) : suggestion ? (
        <button type="button" onClick={() => setLocal(suggestion)}
          className="inline-flex items-center gap-1.5 rounded-pill border-2 border-primary px-3 py-1.5 text-sm font-semibold text-primary hover:bg-primary-soft">
          <Sparkles className="h-4 w-4" /> Use {suggestion}{suffix}
        </button>
      ) : null}
    </div>
  );
}
