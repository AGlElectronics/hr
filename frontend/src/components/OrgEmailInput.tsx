import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';

import { getSettings } from '@/api/settings';
import { renderWithFallback } from '@/lib/naming';

/**
 * Organization email: HR types the part before "@"; the domain and the
 * suggested name template come from settings. A value on another domain
 * switches to a free-text input.
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
  const onDomain = !value || (!!suffix && value.toLowerCase().endsWith(suffix));
  const [custom, setCustom] = useState(false);

  if (!domain || custom || !onDomain) {
    return (
      <div className="space-y-1">
        <input className={className} type="email" value={value} aria-invalid={invalid}
          onChange={(e) => onChange(e.target.value)} placeholder={domain ? `name${suffix}` : 'name@your-org.example'} />
        {domain ? (
          <button type="button" className="text-xs text-primary hover:underline"
            onClick={() => {
              setCustom(false);
              onChange(value.includes('@') ? `${value.split('@')[0]}${suffix}` : value ? `${value}${suffix}` : '');
            }}>
            Use {suffix}
          </button>
        ) : (
          !settings.isLoading && (
            <span className="block text-xs text-muted-foreground">
              Set the organization domain in{' '}
              <Link to="/settings" className="text-primary hover:underline">Settings</Link> to only type the name.
            </span>
          )
        )}
      </div>
    );
  }

  const local = value ? value.slice(0, value.length - suffix.length) : '';
  const suggestion = renderWithFallback(settings.data?.org_email_template, { first: firstName, last: lastName, nickname });
  const setLocal = (raw: string) => {
    // Typing "@" (or pasting a full address) keeps only the part before it.
    const l = raw.split('@')[0].trim();
    onChange(l ? `${l}${suffix}` : '');
  };

  return (
    <div className="space-y-1">
      <div className={`flex items-stretch overflow-hidden rounded-md border border-input bg-background focus-within:ring-2 focus-within:ring-ring ${invalid ? 'border-destructive' : ''}`}>
        <input
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
      <div className="flex flex-wrap gap-x-3 text-xs">
        {!local && suggestion && (
          <button type="button" className="text-primary hover:underline" onClick={() => setLocal(suggestion)}>
            Use {suggestion}{suffix}
          </button>
        )}
        <button type="button" className="text-muted-foreground hover:underline" onClick={() => setCustom(true)}>
          Different domain
        </button>
      </div>
    </div>
  );
}
