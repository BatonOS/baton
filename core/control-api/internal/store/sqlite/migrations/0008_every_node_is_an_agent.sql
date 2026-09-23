


















UPDATE nodes
   SET roles = json_insert(roles, '$[#]', 'agent')
 WHERE roles IS NOT NULL
   AND json_valid(roles)
   AND NOT EXISTS (
     SELECT 1 FROM json_each(nodes.roles) WHERE json_each.value = 'agent'
   );
