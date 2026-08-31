CREATE TABLE library_entries (
    id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id  uuid NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    added_at timestamptz NOT NULL DEFAULT now(),
    notes    text,
    location text,
    UNIQUE (user_id, book_id)
);

CREATE INDEX library_entries_user_id_idx ON library_entries (user_id);
CREATE INDEX library_entries_book_id_idx ON library_entries (book_id);
