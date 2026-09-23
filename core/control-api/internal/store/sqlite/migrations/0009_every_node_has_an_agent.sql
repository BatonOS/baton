


























INSERT INTO identities (identity_id, tenant_id, name, node_id, created_at)
SELECT
    'ident_' || n.node_id,
    n.tenant_id,
    n.display_name,
    n.node_id,
    strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
  FROM nodes n
 WHERE n.status != 'revoked'
   AND NOT EXISTS (SELECT 1 FROM identities i WHERE i.node_id = n.node_id)
   AND NOT EXISTS (SELECT 1 FROM identities i
                    WHERE i.tenant_id = n.tenant_id AND i.name = n.display_name);
