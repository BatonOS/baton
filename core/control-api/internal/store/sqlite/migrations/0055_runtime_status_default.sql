





















ALTER TABLE nodes ADD COLUMN runtime_status_v2 TEXT NOT NULL DEFAULT 'unknown';





UPDATE nodes SET runtime_status_v2 = CASE
  WHEN runtime_status IN ('not-declared','unknown','idle','working','needs-input','done','errored')
    THEN runtime_status
  ELSE 'unknown'
END;

ALTER TABLE nodes DROP COLUMN runtime_status;
ALTER TABLE nodes RENAME COLUMN runtime_status_v2 TO runtime_status;
