CREATE TABLE books (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    isbn       text NOT NULL UNIQUE,
    title      text NOT NULL,
    author     text NOT NULL,
    publisher  text,
    year       integer,
    cover_url  text,
    language   text,
    pages      integer,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX books_title_trgm_idx ON books USING gin (title gin_trgm_ops);
CREATE INDEX books_author_trgm_idx ON books USING gin (author gin_trgm_ops);
