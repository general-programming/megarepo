package gocommon

import (
	"fmt"
	"os"
	"strings"
)

func IsDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return !os.IsNotExist(err)
}

func GetEnvWithDefault(key string, defaultValue string) string {
	value := os.Getenv(key)

	if value == "" {
		return defaultValue
	}

	return value
}

func GetBoolEnvWithDefault(key string, defaultValue bool) bool {
	value := strings.ToLower(os.Getenv(key))

	if value == "" {
		return defaultValue
	}

	return value == "true"
}

func RecoverFunction(funcName string) {
	if r := recover(); r != nil {
		println("[panic] " + funcName + ": " + fmt.Sprintf("%s", r))
	}
}
