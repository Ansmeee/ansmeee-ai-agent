package logger

import (
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// Config holds logger options. It is intentionally decoupled from the app's
// config package so this component stays generic and reusable.
type Config struct {
	Level      string // debug|info|warn|error
	Format     string // json|console
	Output     string // stdout|file
	Filename   string // used when Output==file
	MaxSize    int    // megabytes before rotation
	MaxBackups int    // number of rotated files to retain
	MaxAge     int    // days to retain rotated files
	Compress   bool   // gzip rotated files
}

func defaultConfig() Config {
	return Config{
		Level:      "info",
		Format:     "json",
		Output:     "stdout",
		Filename:   "logs/app.log",
		MaxSize:    100,
		MaxBackups: 7,
		MaxAge:     30,
		Compress:   false,
	}
}

var (
	mu     sync.RWMutex
	global *zap.Logger
)

func init() {
	// Safe default so L() never returns nil before Init.
	l, _ := New(defaultConfig())
	global = l
}

// New builds a configured zap logger. Pure constructor — no global state.
func New(cfg Config) (*zap.Logger, error) {
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", cfg.Level, err)
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Format == "console" {
		encoder = zapcore.NewConsoleEncoder(encCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encCfg)
	}

	var ws zapcore.WriteSyncer
	if cfg.Output == "file" {
		ws = zapcore.AddSync(&lumberjack.Logger{
			Filename:   cfg.Filename,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		})
	} else {
		ws = zapcore.AddSync(os.Stdout)
	}

	core := zapcore.NewCore(encoder, ws, level)
	return zap.New(core, zap.AddCaller()), nil
}

// Init builds a logger from cfg and installs it as the global singleton.
func Init(cfg Config) error {
	l, err := New(cfg)
	if err != nil {
		return err
	}
	mu.Lock()
	global = l
	mu.Unlock()
	return nil
}

// L returns the global logger. Never nil.
func L() *zap.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return global
}

// Info logs at info level via the global logger.
func Info(msg string, fields ...zap.Field) { L().Info(msg, fields...) }

// Error logs at error level via the global logger.
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }

// Debug logs at debug level via the global logger.
func Debug(msg string, fields ...zap.Field) { L().Debug(msg, fields...) }

// Warn logs at warn level via the global logger.
func Warn(msg string, fields ...zap.Field) { L().Warn(msg, fields...) }

// Fatal logs at fatal level via the global logger, then exits.
func Fatal(msg string, fields ...zap.Field) { L().Fatal(msg, fields...) }

// Sync flushes buffered log entries.
func Sync() error { return L().Sync() }
