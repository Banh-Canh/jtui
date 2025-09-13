package utils

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.Logger

func InitializeLogger(logLevel zapcore.Level, logFilePath string) {
	// Create all necessary directories for the log file
	dir := filepath.Dir(logFilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil { // More restrictive permissions
		panic(fmt.Sprintf("Failed to create log directory: %v", err))
	}

	// Optimized encoder config for better performance
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	config := zap.Config{
		Level:       zap.NewAtomicLevelAt(logLevel),
		Development: false,
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},
		Encoding:         "json",
		EncoderConfig:    encoderConfig,
		OutputPaths:      []string{logFilePath},
		ErrorOutputPaths: []string{logFilePath},
	}

	var err error
	Logger, err = config.Build()
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize logger: %v", err))
	}
}

// SyncLogger ensures all log entries are flushed
func SyncLogger() error {
	if Logger != nil {
		return Logger.Sync()
	}
	return nil
}
