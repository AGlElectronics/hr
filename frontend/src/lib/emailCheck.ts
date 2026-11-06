import { useQuery } from '@tanstack/react-query';

import { checkEmail } from '@/api/people';
import { emailError } from '@/lib/validation';

/** Mail-server check for an address; shared by the form and the review step. */
export function useEmailCheck(address: string, enabled = true) {
  const a = address.trim().toLowerCase();
  return useQuery({
    queryKey: ['email-check', a],
    queryFn: () => checkEmail(a),
    enabled: enabled && !!a && !emailError(a),
    staleTime: 10 * 60_000,
    retry: false,
  });
}

export function undeliverableText(address: string): string {
  const domain = address.split('@')[1] ?? address;
  return `No mail server answers for ${domain}. Check the spelling; email to this address will probably bounce.`;
}
