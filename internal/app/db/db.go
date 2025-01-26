package db

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"log/slog"
	"strconv"
	"strings"
)

type Song struct {
	Group       string `json:"group,omitempty"`
	SongName    string `json:"song,omitempty"`
	ReleaseDate string `json:"releaseDate,omitempty"`
	Text        string `json:"text,omitempty"`
	Link        string `json:"link,omitempty"`
}

type Library []Song

type Database struct {
	config *Config
	dbConn *pgxpool.Pool
}

const targetDBver = 20250124172213

func New(config *Config) *Database {
	return &Database{config: config}
}

func (db *Database) Open() error {
	dbConn, err := pgxpool.New(context.Background(), db.config.ConnString())
	if err != nil {
		return err
	}

	err = dbConn.Ping(context.Background())
	if err != nil {
		if strings.Contains(err.Error(), `database "`+db.config.DBName+`" does not exist`) {
			// подразумеваем, что база данных создана администратором СУБД,
			// имеющим соответствующие привилегии
		}
		return err
	}

	db.dbConn = dbConn

	_, err = db.dbConn.Exec(context.Background(), `set datestyle to iso,dmy`)
	if err != nil {
		return err
	}

	err = db.fixDBVersion()
	if err != nil {
		return err
	}

	return nil
}

// устанавливает заданную версию базы данных
func (db *Database) fixDBVersion() error {
	dbSQL := stdlib.OpenDBFromPool(db.dbConn)
	ver, err := goose.GetDBVersion(dbSQL)
	if err != nil {
		return err
	}
	if ver < targetDBver {
		err = goose.UpTo(dbSQL, "./internal/app/db/migrations/", targetDBver)
		if err != nil {
			return err
		}
	}
	if ver > targetDBver {
		err = goose.DownTo(dbSQL, "./internal/app/db/migrations/", targetDBver)
		if err != nil {
			return err
		}
	}
	return nil
}

func (db *Database) ListAllLibrary(s Song, offset, limit string) (Library, error) {
	q := `select groups.author_name, songs.song_name, songs.release_date::text, songs.song_text, songs.link 
from songs inner join groups using (author_id) where (
		($1 = '' or groups.author_name = $1) and
		($2 = '' or songs.song_name = $2) and
		($3 = '' or songs.release_date::text = $3) and
		($4 = '' or songs.song_text = $4) and
		($5 = '' or songs.link = $5)) order by groups.author_name, songs.song_name`

	if offset != "" {
		offsetInt, err := strconv.Atoi(offset)
		if err != nil {
			return nil, err
		}
		q = q + fmt.Sprintf(" offset %d", offsetInt)
	}

	if limit != "" {
		limitInt, err := strconv.Atoi(limit)
		if err != nil {
			return nil, err
		}
		q = q + fmt.Sprintf(" limit %d", limitInt)
	}

	slog.Debug("list all library database query", "filter params", s, "offset", offset, "limit", limit)

	rows, err := db.dbConn.Query(context.Background(), q, s.Group, s.SongName, s.ReleaseDate, s.Text, s.Link)
	defer rows.Close()
	if err != nil {
		return nil, err
	}

	sTmp := Song{}
	lib := make(Library, 0, 64)

	for rows.Next() {
		err = rows.Scan(&sTmp.Group, &sTmp.SongName, &sTmp.ReleaseDate, &sTmp.Text, &sTmp.Link)
		if err != nil {
			return nil, err
		}
		lib = append(lib, sTmp)
	}
	return lib, nil
}

func (db *Database) DeleteSong(author_name, songName string) (string, error) {
	tag, err := db.dbConn.Exec(context.Background(), `delete from songs where song_name=$1 and author_id in (select author_id from groups where author_name=$2)`, songName, author_name)
	slog.Debug("deleting from DB", "db response", tag.String())
	if err != nil {
		return "", err
	}

	return tag.String(), nil
}

func (db *Database) AddSong(s Song) error {
	var id int
	err := db.dbConn.QueryRow(context.Background(), `select (author_id) from groups where author_name=$1`, "author_name_test_1").Scan(&id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		err = db.dbConn.QueryRow(context.Background(), `insert into groups (author_name) values ($1) returning author_id`, s.Group).Scan(&id)
		if err != nil {
			return err
		}
	}

	tag, err := db.dbConn.Exec(context.Background(), `insert into songs (author_id, song_name, release_date, song_text, link) 
values ($1, $2, $3, $4, $5)`, id, s.SongName, s.ReleaseDate, s.Text, s.Link)
	slog.Debug("adding song to db", "db reply", tag.String())
	if err != nil {
		return err
	}
	return nil
}

func (db *Database) UpdateGroupName(author_name string, s Song) error {
	tag, err := db.dbConn.Exec(context.Background(), `update groups
set author_name=$1 where author_name=$2`, s.Group, author_name)
	slog.Debug("updating author_name's name", "db response", tag.String())
	if err != nil {
		return err
	}
	return nil
}

func (db *Database) UpdateSongDetails(author_name, song_name string, s Song) error {
	query := `update songs
set`

	var id int
	if s.Group != "no_data" {
		err := db.dbConn.QueryRow(context.Background(), `select (groups.author_id) from groups where groups.author_name=$1`, s.Group).Scan(&id)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			err = db.dbConn.QueryRow(context.Background(), `insert into groups (author_name) values ($1) returning groups.author_id`, s.Group).Scan(&id)
			if err != nil {
				return err
			}
		}
		strconv.Itoa(id)
		query = query + ` author_id='` + strconv.Itoa(id) + `',`
	}
	if s.SongName != "no_data" {
		query = query + ` song_name='` + s.SongName + `',`
	}
	if s.ReleaseDate != "no_data" {
		query = query + ` release_date='` + s.ReleaseDate + `',`
	}
	if s.Text != "no_data" {
		query = query + ` song_text='` + s.Text + `',`
	}
	if s.Link != "no_data" {
		query = query + ` link='` + s.Link + `',`
	}

	query = query[:len(query)-1] + fmt.Sprintf(` where songs.song_name='%s' and songs.author_id in (select author_id from groups where author_name='%s')`, song_name, author_name)
	tag, err := db.dbConn.Exec(context.Background(), query)
	slog.Debug("updating song details", "db response", tag.String())
	if err != nil {
		return err
	}

	return nil
}

func (db *Database) GetSongText(author_name, songName string) (string, error) {
	row := db.dbConn.QueryRow(context.Background(), `select songs.song_text from songs 
    inner join groups using (author_id) where groups.author_name=$1 and songs.song_name=$2`, author_name, songName)
	var t string
	err := row.Scan(&t)
	if err != nil {
		slog.Error("error retrieving from db", "error", err.Error())
		return "", err
	}

	return t, nil
}
