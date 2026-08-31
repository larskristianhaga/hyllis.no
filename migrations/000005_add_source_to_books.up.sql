ALTER TABLE books ADD COLUMN source text NOT NULL DEFAULT 'manual';

ALTER TABLE books ADD CONSTRAINT books_source_check
    CHECK (source IN ('google_books', 'open_library', 'nb', 'manual'));
