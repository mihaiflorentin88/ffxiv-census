package config

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"os"
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
	Tomestone *TomestoneConfig `mapstructure:"tomestone"`
	Census    *CensusConfig    `mapstructure:"census"`
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
type TomestoneConfig struct {
	APIToken  string  `mapstructure:"api_token"`
	BaseURL   string  `mapstructure:"base_url"`
	RateLimit float64 `mapstructure:"rate_limit"`
	Timeout   string  `mapstructure:"timeout"`
}
type ExpansionConfig struct {
	Name          string `mapstructure:"name"`
	Version       string `mapstructure:"version"`
	FinalQuest    string `mapstructure:"final_quest"`
	Icon          string `mapstructure:"icon"`
	LevelCap      uint32 `mapstructure:"level_cap"`
	AchievementID uint32 `mapstructure:"achievement_id"`
}

type CensusConfig struct {
	ActivityWindowDays int               `mapstructure:"activity_window_days"`
	MaxLevel           uint32            `mapstructure:"max_level"`
	Expansions         []ExpansionConfig `mapstructure:"expansions"`
}

func loadDotEnv() {
	f, err := os.Open(".env")
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func NewConfig() (*Config, error) {
	loadDotEnv()

	content, err := configFS.ReadFile("config.toml")
	if err != nil {
		return nil, fmt.Errorf("read embedded config: %w", err)
	}

	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	v.SetConfigType("toml")
	if err := v.ReadConfig(bytes.NewBuffer(content)); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}
