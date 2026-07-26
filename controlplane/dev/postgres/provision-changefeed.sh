#!/bin/sh
set -eu

publication_name="${PUBLICATION_NAME:-outbox_pub}"
slot_name="${REPLICATION_SLOT_NAME:-outbox_slot}"

# Controlplane modules own table migrations. This infrastructure job waits for
# all sources, then provisions replication DDL with an operator identity rather
# than granting DDL to every Job Orchestrator replica.
while :; do
  ready="$(
    psql -h "${PGHOST}" -U "${PGUSER}" -d "${PGDATABASE}" -Atc \
      "SELECT (
         to_regclass('mail.mail_outbox_records') IS NOT NULL
         AND to_regclass('storage.storage_outbox_records') IS NOT NULL
         AND to_regclass('hypervisor.vm_outbox_records') IS NOT NULL
         AND to_regclass('hierarchy.zones') IS NOT NULL
         AND to_regclass('hierarchy.zone_services') IS NOT NULL
       )::int"
  )"
  if [ "${ready}" = "1" ]; then
    break
  fi
  sleep 2
done

psql \
  -h "${PGHOST}" \
  -U "${PGUSER}" \
  -d "${PGDATABASE}" \
  -v ON_ERROR_STOP=1 \
  -v publication_name="${publication_name}" \
  -v slot_name="${slot_name}" <<'SQL'
SELECT format('CREATE PUBLICATION %I', :'publication_name')
WHERE NOT EXISTS (
  SELECT 1 FROM pg_publication WHERE pubname = :'publication_name'
)
\gexec

SELECT format('ALTER PUBLICATION %I ADD TABLE %I.%I', :'publication_name', source.schema_name, source.table_name)
FROM (
  VALUES
    ('mail', 'mail_outbox_records'),
    ('storage', 'storage_outbox_records'),
    ('hypervisor', 'vm_outbox_records'),
    ('hierarchy', 'zones'),
    ('hierarchy', 'zone_services')
) AS source(schema_name, table_name)
WHERE NOT EXISTS (
  SELECT 1
  FROM pg_publication_tables published
  WHERE published.pubname = :'publication_name'
    AND published.schemaname = source.schema_name
    AND published.tablename = source.table_name
)
\gexec

SELECT pg_create_logical_replication_slot(:'slot_name', 'pgoutput')
WHERE NOT EXISTS (
  SELECT 1 FROM pg_replication_slots WHERE slot_name = :'slot_name'
);
SQL
