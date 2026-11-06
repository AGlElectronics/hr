import { useState } from 'react';
import { AlertTriangle } from 'lucide-react';

import { undeliverableText, useEmailCheck } from '@/lib/emailCheck';
import { noAutofill } from '@/lib/noAutofill';
import { emailError, suggestEmail } from '@/lib/validation';

/**
 * Private email: blocks malformed addresses, suggests fixes for common typos
 * and warns (with "Use anyway") when the domain can't receive mail.
 */
export function EmailInput({
  value,
  onChange,
  acknowledged,
  onAcknowledge,
  invalid,
  inputCls,
}: {
  value: string;
  onChange: (v: string) => void;
  acknowledged: boolean;
  onAcknowledge: (ok: boolean) => void;
  invalid?: boolean;
  inputCls: string;
}) {
  const [blurred, setBlurred] = useState(!!value);
  const check = useEmailCheck(value, blurred);
  const format = blurred ? emailError(value) : undefined;
  const suggestion = blurred && !format ? suggestEmail(value) : undefined;
  const undeliverable = check.data?.deliverable === false;

  return (
    <div className="space-y-1">
      <input
        {...noAutofill}
        type="email"
        required
        className={inputCls}
        value={value}
        aria-invalid={invalid || !!format || (undeliverable && !acknowledged)}
        placeholder="name@example.com"
        onChange={(e) => {
          onChange(e.target.value);
          onAcknowledge(false);
        }}
        onBlur={() => setBlurred(true)}
      />
      {format && <span className="block text-xs text-destructive">{format}</span>}
      {suggestion && (
        <button type="button" className="block text-xs text-primary hover:underline"
          onClick={() => {
            onChange(suggestion);
            onAcknowledge(false);
          }}>
          Did you mean <strong>{suggestion}</strong>?
        </button>
      )}
      {undeliverable && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-warning/40 bg-warning-soft px-2 py-1.5 text-xs text-warning-foreground">
          <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
          <span className="flex-1">{undeliverableText(value)}</span>
          {acknowledged ? (
            <span className="font-semibold">Using it anyway</span>
          ) : (
            <button type="button" onClick={() => onAcknowledge(true)} className="font-semibold underline">Use anyway</button>
          )}
        </div>
      )}
    </div>
  );
}
