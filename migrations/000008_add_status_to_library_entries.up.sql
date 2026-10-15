ALTER TABLE library_entries ADD COLUMN status text NOT NULL DEFAULT 'unread';
ALTER TABLE library_entries ADD CONSTRAINT library_entries_status_check CHECK (status IN ('unread', 'reading', 'read'));
