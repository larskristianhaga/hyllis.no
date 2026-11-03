-- Supabase eksponerer alt i public via PostgREST. Appen bruker kun direkte
-- Postgres-tilkobling (DATABASE_URL), så RLS uten policies låser ute
-- anon/authenticated mens backend-rollen (omgår RLS) fungerer som før.
ALTER TABLE users           ENABLE ROW LEVEL SECURITY;
ALTER TABLE books           ENABLE ROW LEVEL SECURITY;
ALTER TABLE library_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS bun_migrations      ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS bun_migration_locks ENABLE ROW LEVEL SECURITY;
