package logging

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
)

type (
	Level  string
	Format string
)

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

type Config struct {
	Level  Level
	Format Format
}

func LoadConfig() *Config {
	return &Config{
		Level:  Level(strings.ToLower(config.GetEnvOrDefault("LOG_LEVEL", "info"))),
		Format: Format(strings.ToLower(config.GetEnvOrDefault("LOG_FORMAT", "text"))),
	}
}

func (c Config) Validate() error {
	switch c.Level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
	default:
		return fmt.Errorf("logging: invalid LOG_LEVEL %q (want debug|info|warn|error)", c.Level)
	}
	switch c.Format {
	case FormatJSON, FormatText:
	default:
		return fmt.Errorf("logging: invalid LOG_FORMAT %q (want json|text)", c.Format)
	}
	return nil
}

// AddSource reports whether the logger should include caller file:line.
func (c Config) AddSource() bool {
	return c.Level == LevelDebug
}

func (c Config) slogLevel() slog.Level {
	switch c.Level {
	case LevelDebug:
		return slog.LevelDebug
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
