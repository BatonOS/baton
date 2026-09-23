











ALTER TABLE transactions
  ADD COLUMN approval_required_rung TEXT NOT NULL DEFAULT ''
    CHECK (approval_required_rung IN ('', 'button'));

ALTER TABLE transactions
  ADD COLUMN approval_cleared_rung TEXT NOT NULL DEFAULT ''
    CHECK (approval_cleared_rung IN ('', 'button'));
