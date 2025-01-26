-- +goose Up
CREATE TABLE IF NOT EXISTS groups(
    author_id int generated always as identity primary key ,
    author_name text unique not null
);

CREATE TABLE IF NOT EXISTS songs(
    author_id int,
    song_name text,
    release_date date,
    song_text text,
    link text,
primary key (author_id, song_name)
);

create index on songs (
    song_name
);

create index on songs (
    release_date
);
-- при указании primary key или unique на соответствующие колонки
-- автоматически создается индекс

-- +goose Down
DROP TABLE groups;
DROP TABLE songs;
