-- SPDX-License-Identifier: Apache-2.0
























INSERT INTO grants (tenant_id, grant_id, grantor, grantee, action, object,
                    scope, effect, constraints, valid_from, valid_until, proof, created_at)
SELECT n.tenant_id,
       'grt_cap_' || cr.capability_id,
       cr.decided_by,
       '*',
       'baton.capability.invoke',
       cr.capability_id,
       'network:self',
       'deny',
       'legacy:capability_revocations',
       cr.decided_at,
       '',
       x'',
       cr.decided_at
  FROM capability_revocations cr
  JOIN capabilities c ON c.id = cr.capability_id
  JOIN nodes n        ON n.node_id = c.node_id;

DROP TABLE capability_revocations;
