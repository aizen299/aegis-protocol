-- The nonce a node signed over. Without it the stored signature cannot be verified off-chain, and
-- an unverifiable signature is decorative — the aggregation service exists to check these
-- independently of the contract that already accepted them.
--
-- Nullable because rows indexed before SubmissionReceived carried the nonce have no value to
-- backfill from. Verification treats a missing nonce as unverifiable rather than assuming zero,
-- which would silently reject every historical row as a forged signature.
ALTER TABLE oracle_submissions ADD COLUMN nonce NUMERIC(78, 0)
    CHECK (nonce IS NULL OR nonce >= 0);
