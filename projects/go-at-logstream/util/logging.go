package util

import (
	"context"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	LoggerKey = "K_LOGGER"
)

func CreateLogger(d bool) *zap.Logger {

	pe := zap.NewProductionEncoderConfig()
	pe.EncodeTime = zapcore.ISO8601TimeEncoder

	level := zap.InfoLevel
	if d {
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

func LogWithCtx(ctx context.Context) *zap.Logger {
	logger, ok := ctx.Value(LoggerKey).(*zap.Logger)
	if !ok {
		return CreateLogger(false)
	}

	return logger
}