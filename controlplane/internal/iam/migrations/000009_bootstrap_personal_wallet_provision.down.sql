-- The Cost projection may already have consumed these commands, so rollback
-- can remove only commands that have never crossed the IAM durable boundary.
DELETE FROM lifecycle_fact_outbox_records
WHERE status = 'PENDING'
  AND event_id IN (
      '59002562-bee4-5f1a-aa66-6a0a9e0efcfa'::uuid,
      'cb70c09c-dfee-5a92-b224-85eaf583117e'::uuid,
      '5d112cc7-128d-5598-a6c6-376e9c73642f'::uuid,
      '66191a81-8a5a-590d-a961-5fa0716c7fbc'::uuid,
      '81b33b0c-65a1-538f-9b36-62eeb548b471'::uuid
  );
