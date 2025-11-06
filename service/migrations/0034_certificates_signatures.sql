-- Optional cryptographic signatures for certificates

ALTER TABLE certificates
  ADD COLUMN IF NOT EXISTS signature TEXT,
  ADD COLUMN IF NOT EXISTS signature_alg TEXT;

