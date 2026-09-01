ALTER TABLE books DROP CONSTRAINT books_source_check;

ALTER TABLE books ADD CONSTRAINT books_source_check
    CHECK (source IN ('google_books', 'open_library', 'nb', 'manual'));
