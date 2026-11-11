-- Shared foundations:
--   - den_link.revoked_at: set when Den rejects HR's credential, so the Den
--     card can say "revoked, pair again" (shared denlink SchemaUpgrade).
--   - Retention periods are stored in days (Den's retention settings are in
--     days): retention_*_months become retention_*_days.

-- +goose Up
ALTER TABLE den_link ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

UPDATE settings
   SET key = replace(key, '_months', '_days'),
       value = round(value::numeric * 365 / 12)::int::text
 WHERE key IN ('retention_applicant_months', 'retention_paused_months', 'retention_offboarded_audit_months')
   AND value ~ '^[0-9]+$';
DELETE FROM settings WHERE key IN ('retention_applicant_months', 'retention_paused_months', 'retention_offboarded_audit_months');

-- +goose Down
UPDATE settings
   SET key = replace(key, '_days', '_months'),
       value = greatest(1, round(value::numeric * 12 / 365))::int::text
 WHERE key IN ('retention_applicant_days', 'retention_paused_days', 'retention_offboarded_audit_days')
   AND value ~ '^[0-9]+$';
ALTER TABLE den_link DROP COLUMN IF EXISTS revoked_at;
