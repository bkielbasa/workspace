ALTER TABLE users DROP COLUMN IF EXISTS organization_id;
DROP TABLE IF EXISTS organization_sso;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organization_domains;
DROP TABLE IF EXISTS organizations;
