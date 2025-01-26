package apiserver

import (
	"ApiServer/internal/app/db"
	"os"
)

type Config struct {
	BindPort string
	LogLevel string
	Database *db.Config
}

func NewConfig() *Config {
	return &Config{
		BindPort: os.Getenv("BIND_PORT"),
		LogLevel: os.Getenv("LOG_LEVEL"),
		Database: db.NewConfig(),
	}
}
