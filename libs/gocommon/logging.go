package gocommon

import (
	"context"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	LoggerKey = "K_LOGGER"
)

func CreateLogger(debug bool) *zap.Logger {
	pe := zap.NewProductionEncoderConfig()
	pe.EncodeTime = zapcore.ISO8601TimeEncoder

	level := zap.InfoLevel
	if debug {
		level = zap.DebugLevel
	}

	var encoder zapcore.Encoder
	if IsDocker() {
		encoder = zapcore.NewJSONEncoder(pe)
	} else {
		encoder = zapcore.NewConsoleEncoder(pe)
	}

	core := zapcore.NewTee(
		zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level),
	)

	l := zap.New(core)

	return l
}

func CtxWithLogger(ctx context.Context) context.Context {
	return context.WithValue(ctx, LoggerKey, CreateLogger(false))
}

func LogWithCtx(ctx context.Context) *zap.Logger {
	logger, ok := ctx.Value(LoggerKey).(*zap.Logger)
	if !ok {
		return CreateLogger(false)
	}

	return logger
}
