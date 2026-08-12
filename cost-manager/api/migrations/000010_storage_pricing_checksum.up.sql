-- 000009 changes the storage catalog from MB thresholds to decimal GB_HOUR.
-- Keep the catalog checksum synchronized with those ordered range values so
-- the API cache and Rust engine share the same pricing snapshot.
UPDATE billing.tier_versions
SET checksum = '803fd4d7f6d568df426e47f8fed909ba8cdd8005cf036ec6a4a20e24c13cdbbe'
WHERE id = 'b33aa15e-0421-4185-658b-f0b8132c1723'
  AND checksum = 'a4f31566a87f657cf0781b7b92f7aa9ccbb081d269dc66590cc7e2bbc0e8476e';
