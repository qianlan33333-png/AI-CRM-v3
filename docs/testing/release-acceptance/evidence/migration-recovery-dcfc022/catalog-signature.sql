WITH catalog_rows AS (
  SELECT 'column' AS kind, n.nspname, c.relname, a.attnum::text AS ordinal,
         a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod),
         a.attnotnull::text, COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') AS detail
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
  WHERE n.nspname = 'public' AND c.relkind IN ('r','p') AND a.attnum > 0 AND NOT a.attisdropped
  UNION ALL
  SELECT 'constraint', n.nspname, c.relname, con.conname, con.contype::text,
         pg_get_constraintdef(con.oid, true), con.condeferrable::text,
         con.convalidated::text
  FROM pg_constraint con
  JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public'
  UNION ALL
  SELECT 'index', n.nspname, c.relname, i.relname, ix.indisunique::text,
         pg_get_indexdef(ix.indexrelid), COALESCE(pg_get_expr(ix.indpred, ix.indrelid), ''),
         ''
  FROM pg_index ix
  JOIN pg_class c ON c.oid = ix.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_class i ON i.oid = ix.indexrelid WHERE n.nspname = 'public'
)
SELECT count(*) || ':' || md5(string_agg(concat_ws('|', kind,nspname,relname,ordinal,attname,format_type,attnotnull,detail), E'\n' ORDER BY kind,nspname,relname,ordinal,attname)) AS catalog_signature
FROM catalog_rows;
