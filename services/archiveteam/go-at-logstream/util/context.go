package util

import "context"

func CreateContext() context.Context {
	ctx := context.WithValue(context.TODO(), LoggerKey, CreateLogger())

	return ctx
}
