package apiserver

import (
	"ApiServer/internal/app/db"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

type APIServer struct {
	config   *Config
	router   *mux.Router
	database *db.Database
	server   *http.Server
}

func NewAPIServer(config *Config) *APIServer {
	return &APIServer{
		config: config,
		router: mux.NewRouter(),
		server: &http.Server{
			Addr:         config.BindPort,
			ReadTimeout:  time.Second * 15,
			WriteTimeout: time.Second * 15,
		},
	}
}

func (s *APIServer) Start() error {
	slog.Debug("debug is enabled")

	s.configureRouter()
	s.server.Handler = s.router

	err := s.configureDB()
	if err != nil {
		return err
	}

	idleConnsClosed := make(chan struct{})

	// горутина для перехвата SIGINT и graceful shutdown работы сервера
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		ctxTO, cancel := context.WithTimeout(context.Background(), time.Second*15)
		defer cancel()
		if err := s.server.Shutdown(ctxTO); err != nil {
			slog.Error("HTTP server Shutdown", "error", err.Error())
		}
		close(idleConnsClosed)
	}()

	slog.Info("starting api server")
	slog.Debug("server data", "port", s.config.BindPort)

	if err := s.server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("error starting or closing listener", "error", err.Error())
		return err
	}

	<-idleConnsClosed

	slog.Info("api server stopped gracefully")
	return nil
}

func (s *APIServer) configureRouter() {
	s.router.HandleFunc("/library/all", s.listLibrary()).Methods("GET")
	s.router.HandleFunc("/library/text", s.showSongText()).Methods("GET")
	s.router.HandleFunc("/library/delete", s.deleteSong()).Methods("DELETE")
	s.router.HandleFunc("/library/add", s.addSong()).Methods("POST")
	s.router.HandleFunc("/library/update", s.updateSong()).Methods("PATCH")
}

func (s *APIServer) configureDB() error {
	slog.Debug("Database connection string: " + s.config.Database.ConnString())
	database := db.New(s.config.Database)
	err := database.Open()
	if err != nil {
		return err
	}

	s.database = database
	return nil
}

// функция парсит параметры запроса и выдаёт отфильтрованный
// на их основе лист песен
// если параметр не указан - фильтрация по нему не происходит.
func (s *APIServer) listLibrary() http.HandlerFunc {
	var lib db.Library
	var filterParams db.Song
	var offset, limit string
	var err error
	return func(writer http.ResponseWriter, request *http.Request) {
		slog.Info("list library request", "from", request.RemoteAddr, "to", request.Host+request.URL.String())
		writer.Header().Set("Content-type", "application/json")

		filterParams.Group = request.FormValue("author")
		filterParams.SongName = request.FormValue("song")
		filterParams.ReleaseDate = request.FormValue("releaseDate")
		filterParams.Text = request.FormValue("text")
		filterParams.Link = request.FormValue("link")
		offset = request.FormValue("offset")
		limit = request.FormValue("limit")

		slog.Debug("filter parameters", "struct", filterParams, "offset", offset, "limit", limit)

		lib, err = s.database.ListAllLibrary(filterParams, offset, limit)
		if err != nil {
			writer.WriteHeader(500)
			slog.Error("error retrieving from db", "error", err.Error())
			return
		}

		if len(lib) == 0 {
			writer.WriteHeader(404)
			slog.Debug("song not found", "provided URL", request.URL)
			return
		}
		encoder := json.NewEncoder(writer)
		encoder.Encode(lib)
	}
}

func (s *APIServer) deleteSong() http.HandlerFunc {
	var author, songName string

	return func(writer http.ResponseWriter, request *http.Request) {
		slog.Info("delete song request", "from", request.RemoteAddr,
			"to", request.Host+request.URL.String())

		author = request.FormValue("author")
		songName = request.FormValue("song")
		slog.Debug("delete request", "author", author, "song", songName)

		// необходимы оба поля author и song для точного определения песни,
		// которую необходимо удалить
		if author == "" || songName == "" {
			slog.Error("bad request, author and/or name of the song weren't provided",
				"request", request.Host+request.URL.String())
			writer.WriteHeader(400)
			return
		}

		dbresp, err := s.database.DeleteSong(author, songName)
		if err != nil {
			slog.Error("error deleting from database", "error", err.Error())
			writer.WriteHeader(500)
		}
		if dbresp == "DELETE 0" {
			writer.WriteHeader(404)
			return
		}
	}
}

// вывод текста определенной песни, с возможностью выбора куплета
func (s *APIServer) showSongText() http.HandlerFunc {
	var author, song, text, verse string
	var err error

	return func(writer http.ResponseWriter, request *http.Request) {
		slog.Info("song text request", "from", request.RemoteAddr, "to", request.Host+request.URL.String())
		author = request.FormValue("author")
		song = request.FormValue("song")
		verse = request.FormValue("verse")

		slog.Debug("", "author", author, "song", song, "verse", verse)

		// необходимы оба поля author и song для точного определения песни,
		// текст которой необходимо показать
		if author == "" || song == "" {
			slog.Error("bad request, author and/or name of the song weren't provided",
				"request", request.Host+request.URL.String())
			writer.WriteHeader(400)
			return
		}

		// подразумеваем, что куплеты песни разделены между собой
		// одной пустой строкой
		text, err = s.database.GetSongText(author, song)
		slog.Debug("", "text", text)
		if err != nil {
			slog.Error("error retrieving from db", "error", err.Error())
			writer.WriteHeader(500)
			return
		}
		tmp := strings.Split(text, "\n\n")

		// если параметр verse не указан (или указан как 0) - выводим весь текст
		// в другом случае выводим указанный куплет (1 = первый куплет, и т.д.)
		// отрицательное значение куплета, а также значение, превышающее кол-во
		// куплетов в песне = bad request
		if verse == "" {
			fmt.Fprint(writer, text)
			return
		}
		verseInt, err := strconv.Atoi(verse)
		if err != nil {
			slog.Error(err.Error())
			writer.WriteHeader(400)
			return
		}
		if verseInt > len(tmp) || verseInt < 0 {
			slog.Error("bad request", "verse", verseInt)
			writer.WriteHeader(400)
			return
		}
		if verseInt == 0 {
			fmt.Fprint(writer, text)
		} else {
			fmt.Fprint(writer, tmp[verseInt-1])
		}
	}
}

func (s *APIServer) addSong() http.HandlerFunc {
	var song db.Song
	var body []byte
	var err error
	var reqURL string
	var resp *http.Response
	externalURL := os.Getenv("EXTERNAL_API_URL")

	return func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		slog.Info("add song request", "from", request.RemoteAddr, "to", request.Host+request.URL.String())
		body, err = io.ReadAll(request.Body)
		if err != nil {
			slog.Error("error reading request body", "error", err.Error())
			writer.WriteHeader(400)
			return
		}

		err = json.Unmarshal(body, &song)
		if err != nil {
			writer.WriteHeader(400)
			slog.Error("error unmarshalling", "error", err.Error())
			return
		}
		slog.Debug("request body", "struct", song)

		reqURL = fmt.Sprintf("%s?group=%s&song=%s", externalURL, song.Group, song.SongName)
		slog.Debug("accessing external api", "URL", reqURL)
		timer := time.Second

		// повторяем запрос вплоть до 5 раз в случае получения кода 500
		// в других случаях либо мы получили что и хотели, либо ошибка на нашей стороне,
		// либо ошибка нам неизвестна
	outer:
		for range 5 {
			resp, err = http.Get(reqURL)
			if err != nil {
				writer.WriteHeader(500)
				slog.Error("http.get error", "error", err.Error())
				fmt.Fprint(writer, "error trying to access external api: "+err.Error())
				resp.Body.Close()
				return
			}
			defer resp.Body.Close()
			switch resp.StatusCode {
			case 400:
				slog.Error("received code 400, bad request")
				writer.WriteHeader(400)
				return
			case 500:
				slog.Debug("received code 500, trying to get " + reqURL + " again")
				time.Sleep(timer)
				timer = min(timer*2, time.Second*10)
			case 200:
				break outer
			default:
				// исходя из ТЗ мы никогда не должны сюда попасть
				writer.WriteHeader(resp.StatusCode)
				slog.Error("got unsupported response code", "code", resp.StatusCode)
				return
			}
		}

		defer resp.Body.Close()

		slog.Debug("response from external api", "resp code", resp.StatusCode)

		if resp.StatusCode == 500 {
			slog.Error("external api is not working")
			writer.WriteHeader(500)
			fmt.Fprint(writer, "external api is not working")
			return
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("error reading external api response body", "error", err.Error())
			return
		}
		err = json.Unmarshal(data, &song)
		if err != nil {
			slog.Error("error unmarshalling", "error", err.Error())
			return
		}
		slog.Debug("adding song to database", "song struct", song)
		err = s.database.AddSong(song)
		if err != nil {
			slog.Error("error adding to the database", "error", err.Error())
			writer.WriteHeader(500)
		}
	}
}

// обновление данных песни или исполнителя
// если и в квери, и в теле указан только исполнитель,
// то будет обновлено имя исполнителя в таблице groups
// (например для устранения опечатки)
// в любом другом случае в квери также необходимо указать название песни
// и будут обновляться данные песни
func (s *APIServer) updateSong() http.HandlerFunc {
	// стандартные значения установлени на no_data для реализации возможности удаления каких-либо
	// данных о песне (другими словами - заменой их на пустую строку)
	song := db.Song{
		Group:       "no_data",
		SongName:    "no_data",
		ReleaseDate: "no_data",
		Text:        "no_data",
		Link:        "no_data",
	}
	var body []byte
	var err error

	return func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		slog.Info("update song request", "from", request.RemoteAddr,
			"to", request.Host+request.URL.String())
		author := request.FormValue("author")
		songname := request.FormValue("song")

		if author == "" {
			slog.Error("bad request, author wasn't provided",
				"request", request.Host+request.URL.String())
			writer.WriteHeader(400)
			return
		}

		slog.Debug("update", "author", author, "song name", songname)

		body, err = io.ReadAll(request.Body)
		if err != nil {
			slog.Error("error reading request body", "error", err.Error())
			writer.WriteHeader(400)
			return
		}

		err = json.Unmarshal(body, &song)
		if err != nil {
			slog.Error("unmarshal error", "error", err.Error())
			writer.WriteHeader(400)
			return
		}

		slog.Debug("", "update song data", song)

		// проверяем если в теле находятся только данные об имени исполнителя,
		// а также что в квери указан только автор
		if songname == "" && song.Group != "no_data" && song.SongName == "no_data" && song.Link == "no_data" && song.ReleaseDate == "no_data" && song.Text == "no_data" {
			err = s.database.UpdateGroupName(author, song)
			if err != nil {
				slog.Error("error updating author's name", "err", err.Error())
				return
			}
			return
		}

		// на этом этапе требуем название песни, т.к. будут меняться её данные
		// и необходимо знать в какой песне их менять
		if songname == "" {
			slog.Error("bad request, song name wasn't provided",
				"request", request.Host+request.URL.String())
			writer.WriteHeader(400)
			return
		}

		err = s.database.UpdateSongDetails(author, songname, song)
		if err != nil {
			slog.Error("updating song details error", "error", err.Error())
			writer.WriteHeader(500)
			return
		}
	}
}
