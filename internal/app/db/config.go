package db

import (
	"fmt"
	"os"
)

type Config struct {
	Host     string
	Port     string
	DBName   string
	User     string
	Password string
}

func NewConfig() *Config {
	return &Config{
		Host:     os.Getenv("DB_HOST"),
		Port:     os.Getenv("DB_PORT"),
		DBName:   os.Getenv("DB_NAME"),
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
	}
}

func (c *Config) ConnString() string {
	return fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s", c.Host, c.Port, c.DBName, c.User, c.Password)
}
