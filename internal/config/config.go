package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all configuration for the application.
type Config struct {
	Server ServerConfig `mapstructure:"server"`
	LLM    LLMConfig    `mapstructure:"llm"`
	MySQL  MySQLConfig  `mapstructure:"mysql"`
	Redis  RedisConfig  `mapstructure:"redis"`
	Memory MemoryConfig `mapstructure:"memory"`
	Milvus Milvus       `mapstructure:"milvus"`
	Agent  AgentConfig  `mapstructure:"agent"`
	Log    LogConfig    `mapstructure:"log"`
}

// LogConfig is the logger configuration.
type LogConfig struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`
	Output     string `mapstructure:"output"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
	Compress   bool   `mapstructure:"compress"`
}

// ServerConfig is the HTTP server configuration.
type ServerConfig struct {
	Port       int    `mapstructure:"port"`
	Mode       string `mapstructure:"mode"`
	JWTSecret  string `mapstructure:"jwt_secret"`
	CORSOrigin string `mapstructure:"cors_origin"`
}

// LLMConfig is the LLM provider configuration.
type LLMConfig struct {
	Provider    string        `mapstructure:"provider"`
	APIKey      string        `mapstructure:"api_key"`
	BaseURL     string        `mapstructure:"base_url"`
	Model       string        `mapstructure:"model"`
	Temperature float64       `mapstructure:"temperature"`
	MaxTokens   int           `mapstructure:"max_tokens"`
	Timeout     time.Duration `mapstructure:"timeout"`
}

type Milvus struct {
	Address       string `mapstructure:"address"`
	Username      string `mapstructure:"username"`
	Password      string `mapstructure:"password"`
	DBName        string `mapstructure:"dbname"`
	Collection    string `mapstructure:"collection"`
	TextMaxLength int64  `mapstructure:"text_max_length"`
}

// MySQLNodeConfig is a single MySQL node configuration.
type MySQLNodeConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	Database        string        `mapstructure:"database"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

// DSN returns the MySQL data source name.
func (c MySQLNodeConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

// MySQLConfig is the MySQL master-slave configuration.
type MySQLConfig struct {
	Master MySQLNodeConfig `mapstructure:"master"`
	Slave  MySQLNodeConfig `mapstructure:"slave"`
}

// RedisConfig is the Redis connection configuration.
type RedisConfig struct {
	Addr       string `mapstructure:"addr"`
	Password   string `mapstructure:"password"`
	DB         int    `mapstructure:"db"`
	MaxRetries int    `mapstructure:"max_retries"`
	PoolSize   int    `mapstructure:"pool_size"`
}

// MemoryConfig is the memory backend configuration.
type MemoryConfig struct {
	Type        string        `mapstructure:"type"`
	TTL         time.Duration `mapstructure:"ttl"`
	MaxMessages int           `mapstructure:"max_messages"`
}

// AgentConfig is the Agent engine configuration.
type AgentConfig struct {
	MaxIterations      int           `mapstructure:"max_iterations"`
	ToolTimeout        time.Duration `mapstructure:"tool_timeout"`
	MaxOutputLength    int           `mapstructure:"max_output_length"`
	ParallelToolCalls  bool          `mapstructure:"parallel_tool_calls"`
	MaxContextMessages int           `mapstructure:"max_context_messages"`
}

// envReplacer maps viper keys to env vars: llm.api_key → LLM_API_KEY.
type envReplacer struct{}

func (envReplacer) Replace(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, ".", "_"))
}

// Load reads configuration from a YAML file, with env var overrides.
// .env is loaded automatically if present. Env vars take precedence over YAML.
func Load(path string) (*Config, error) {
	_ = godotenv.Load()

	v := viper.NewWithOptions(viper.EnvKeyReplacer(envReplacer{}))
	v.SetConfigFile(path)
	v.AutomaticEnv()

	v.SetDefault("agent.parallel_tool_calls", true)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	applyMySQLDefaults(&c.MySQL.Master)
	if c.MySQL.Slave.Host == "" {
		c.MySQL.Slave = c.MySQL.Master
	}
	applyMySQLDefaults(&c.MySQL.Slave)

	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.Mode == "" {
		c.Server.Mode = "debug"
	}
	if c.Server.JWTSecret == "" {
		c.Server.JWTSecret = "ai-agent-secret-key-change-in-production"
	}
	if c.Server.CORSOrigin == "" {
		c.Server.CORSOrigin = "*"
	}
	if c.LLM.Temperature == 0 {
		c.LLM.Temperature = 0.7
	}
	if c.LLM.MaxTokens == 0 {
		c.LLM.MaxTokens = 4096
	}
	if c.LLM.Timeout == 0 {
		c.LLM.Timeout = 60 * time.Second
	}
	if c.Memory.Type == "" {
		c.Memory.Type = "memory"
	}
	if c.Memory.TTL == 0 {
		c.Memory.TTL = 30 * time.Minute
	}
	if c.Memory.MaxMessages == 0 {
		c.Memory.MaxMessages = 100
	}

	if c.Agent.MaxIterations == 0 {
		c.Agent.MaxIterations = 5
	}
	if c.Agent.ToolTimeout == 0 {
		c.Agent.ToolTimeout = 30 * time.Second
	}
	if c.Agent.MaxOutputLength == 0 {
		c.Agent.MaxOutputLength = 4096
	}
	if c.Agent.MaxContextMessages == 0 {
		c.Agent.MaxContextMessages = 50
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		if c.Server.Mode == "debug" {
			c.Log.Format = "console"
		} else {
			c.Log.Format = "json"
		}
	}
	if c.Log.Output == "" {
		c.Log.Output = "stdout"
	}
	if c.Log.Filename == "" {
		c.Log.Filename = "logs/app.log"
	}
	if c.Log.MaxSize == 0 {
		c.Log.MaxSize = 100
	}
	if c.Log.MaxBackups == 0 {
		c.Log.MaxBackups = 7
	}
	if c.Log.MaxAge == 0 {
		c.Log.MaxAge = 30
	}
}

func applyMySQLDefaults(c *MySQLNodeConfig) {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 3306
	}
	if c.User == "" {
		c.User = "root"
	}
	if c.Database == "" {
		c.Database = "ai_agent"
	}
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = 25
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = 10
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = 5 * time.Minute
	}
}
