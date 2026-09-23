-- SPDX-License-Identifier: Apache-2.0
























INSERT INTO grants (tenant_id, grant_id, grantor, grantee, action, object,
                    scope, effect, constraints, valid_from, valid_until, proof, created_at)
SELECT ag.tenant_id,
       ag.grant_id,
       COALESCE((SELECT n.network_id FROM networks n WHERE n.tenant_id = ag.tenant_id), ''),
       ag.subject_network_id,
       'baton.resource.fetch',
       '',
       ag.scope,
       'allow',
       'subject_key:' || ag.subject_key,
       ag.issued_at,
       ag.expiry_at,
       x'',
       ag.issued_at
  FROM access_grants ag;

INSERT INTO grant_revocations (tenant_id, grant_id, revoked_by, revoked_at)
SELECT ag.tenant_id, ag.grant_id, '', ag.revoked_at
  FROM access_grants ag
 WHERE ag.revoked_at IS NOT NULL;

DROP TABLE access_grants;
