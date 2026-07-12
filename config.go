package main

import "os"

type config struct {
	httpAddr    string
	databaseURL string
}

func loadConfig() config {
	cfg := config{
		httpAddr:    getEnv("HTTP_ADDR", ":8080"),
		databaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/workspace?sslmode=disable"),
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	return value
}
