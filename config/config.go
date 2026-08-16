package config

import (
	"bytes"
	"embed"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

//go:embed config.toml
var configFS embed.FS

type Config struct {
	App       AppConfig        `mapstructure:"app"`
	Logging   *LoggingConfig   `mapstructure:"logging"`
	HTTP      HTTPConfig       `mapstructure:"http"`
	Auth      *AuthConfig      `mapstructure:"auth"`
	Metrics   *MetricsConfig   `mapstructure:"metrics"`
	SQLite    *SQLiteConfig    `mapstructure:"sqlite"`
	Queue     *QueueConfig     `mapstructure:"queue"`
	Lodestone *LodestoneConfig `mapstructure:"lodestone"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Env  string `mapstructure:"env"`
}

type LoggingConfig struct {
	Default       string `mapstructure:"default"`
	ServerDefault string `mapstructure:"server_default"`
	Level         string `mapstructure:"level"`
}

type HTTPConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	ReadTimeout  string `mapstructure:"read_timeout"`
	WriteTimeout string `mapstructure:"write_timeout"`
	IdleTimeout  string `mapstructure:"idle_timeout"`
}

type AuthConfig struct {
	Enabled bool     `mapstructure:"enabled"`
	Token   string   `mapstructure:"token"`
	Routes  []string `mapstructure:"routes"`
	Users   []string `mapstructure:"users"`
}
type MetricsConfig struct {
	Endpoint string `mapstructure:"endpoint"`
	Prefix   string `mapstructure:"prefix"`
}
type SQLiteConfig struct {
	Path         string `mapstructure:"path"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
	BusyTimeout  string `mapstructure:"busy_timeout"`
	JournalMode  string `mapstructure:"journal_mode"`
}
type QueueConfig struct {
	ClaimBatchSize     int `mapstructure:"claim_batch_size"`
	MaxAttempts        int `mapstructure:"max_attempts"`
	BackoffBaseSeconds int `mapstructure:"backoff_base_seconds"`
}
type LodestoneConfig struct {
	RateLimit  float64 `mapstructure:"rate_limit"`
	MaxRetries int     `mapstructure:"max_retries"`
}

func NewConfig() (*Config, error) {
	content, err := configFS.ReadFile("config.toml")
	if err != nil {
		return nil, fmt.Errorf("read embedded config: %w", err)
	}

	v := viper.New()
	v.SetConfigType("toml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadConfig(bytes.NewBuffer(content)); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}
