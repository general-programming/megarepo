package util

import (
	"net/http"
	_ "net/http/pprof"
	"os"
)

func StartDebugServer() {
	go func() {
		http.ListenAndServe(":6060", nil)
	}()
}

func GetEnvWithDefault(key string, defaultValue string) string {
	value := os.Getenv(key)

	if value == "" {
		return defaultValue
	}

	return value
}
