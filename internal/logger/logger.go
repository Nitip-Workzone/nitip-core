package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New creates a zap logger with a human-readable console format in development
// and JSON structured format in production.
func New(isDev bool) (*zap.Logger, error) {
	var encoder zapcore.Encoder

	encoderCfg := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder, // colored level: INFO, ERROR
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	if isDev {
		// Readable console output for development
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	} else {
		// JSON for production log aggregators
		encoderCfg.EncodeLevel = zapcore.LowercaseLevelEncoder
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	}

	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(os.Stdout),
		zapcore.DebugLevel,
	)

	return zap.New(core, zap.AddCaller(), zap.AddCallerSkip(0)), nil
}
