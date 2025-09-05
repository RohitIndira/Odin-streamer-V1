package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	// Server Configuration
	Server ServerConfig `json:"server"`

	// Database Configuration
	Database DatabaseConfig `json:"database"`

	// External APIs
	APIs APIConfig `json:"apis"`

	// Application Settings
	App AppConfig `json:"app"`

	// Logging Configuration
	Logging LoggingConfig `json:"logging"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port         string        `json:"port"`
	Host         string        `json:"host"`
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	IdleTimeout  time.Duration `json:"idle_timeout"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	TimescaleDB TimescaleDBConfig `json:"timescale_db"`
	Redis       RedisConfig       `json:"redis"`
	SQLite      SQLiteConfig      `json:"sqlite"`
}

// TimescaleDBConfig holds TimescaleDB configuration
type TimescaleDBConfig struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Database     string `json:"database"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	SSLMode      string `json:"ssl_mode"`
	MaxOpenConns int    `json:"max_open_conns"`
	MaxIdleConns int    `json:"max_idle_conns"`
	URL          string `json:"url"` // Full connection URL
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Database int    `json:"database"`
	URL      string `json:"url"` // Full connection URL
}

// SQLiteConfig holds SQLite configuration
type SQLiteConfig struct {
	Path string `json:"path"`
}

// APIConfig holds external API configuration
type APIConfig struct {
	StockAPI      StockAPIConfig      `json:"stock_api"`
	HistoricalAPI HistoricalAPIConfig `json:"historical_api"`
}

// StockAPIConfig holds stock API configuration
type StockAPIConfig struct {
	BaseURL    string        `json:"base_url"`
	Timeout    time.Duration `json:"timeout"`
	RetryCount int           `json:"retry_count"`
	RateLimit  int           `json:"rate_limit"` // requests per minute
}

// HistoricalAPIConfig holds historical API configuration
type HistoricalAPIConfig struct {
	BaseURL    string        `json:"base_url"`
	Timeout    time.Duration `json:"timeout"`
	RetryCount int           `json:"retry_count"`
}

// AppConfig holds application-specific configuration
type AppConfig struct {
	Environment     string        `json:"environment"`
	Debug           bool          `json:"debug"`
	TestMode        bool          `json:"test_mode"` // Allow processing outside market hours for testing
	PythonScript    string        `json:"python_script"`
	MarketOpen      time.Duration `json:"market_open"`  // 09:15 IST
	MarketClose     time.Duration `json:"market_close"` // 15:30 IST
	LateTolerance   time.Duration `json:"late_tolerance"`
	CacheExpiration time.Duration `json:"cache_expiration"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level      string `json:"level"`
	Format     string `json:"format"`
	Output     string `json:"output"`
	MaxSize    int    `json:"max_size"` // MB
	MaxBackups int    `json:"max_backups"`
	MaxAge     int    `json:"max_age"` // days
}

// Load loads configuration from environment variables and files
func Load() (*Config, error) {
	// Try to load .env files in order of preference
	envFiles := []string{
		"configs/production.env",
		"configs/streamer.env",
		"configs/.env.example",
		".env",
	}

	for _, envFile := range envFiles {
		if _, err := os.Stat(envFile); err == nil {
			if err := godotenv.Load(envFile); err == nil {
				break // Successfully loaded
			}
		}
	}

	config := &Config{
		Server: ServerConfig{
			Port:         getEnvOrDefault("WS_PORT", "8080"),
			Host:         getEnvOrDefault("HOST", "0.0.0.0"),
			ReadTimeout:  getDurationOrDefault("READ_TIMEOUT", 30*time.Second),
			WriteTimeout: getDurationOrDefault("WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:  getDurationOrDefault("IDLE_TIMEOUT", 120*time.Second),
		},
		Database: DatabaseConfig{
			TimescaleDB: TimescaleDBConfig{
				Host:         getEnvOrDefault("TIMESCALE_HOST", "localhost"),
				Port:         getIntOrDefault("TIMESCALE_PORT", 5432),
				Database:     getEnvOrDefault("TIMESCALE_DB", "odin_streamer"),
				Username:     getEnvOrDefault("TIMESCALE_USER", "odin_user"),
				Password:     getEnvOrDefault("TIMESCALE_PASSWORD", "odin_password"),
				SSLMode:      getEnvOrDefault("TIMESCALE_SSLMODE", "disable"),
				MaxOpenConns: getIntOrDefault("TIMESCALE_MAX_OPEN_CONNS", 25),
				MaxIdleConns: getIntOrDefault("TIMESCALE_MAX_IDLE_CONNS", 5),
				URL:          getEnvOrDefault("TIMESCALE_URL", "postgres://odin_user:odin_password@localhost:5432/odin_streamer?sslmode=disable"),
			},
			Redis: RedisConfig{
				Host:     getEnvOrDefault("REDIS_HOST", "localhost"),
				Port:     getIntOrDefault("REDIS_PORT", 6379),
				Password: getEnvOrDefault("REDIS_PASSWORD", ""),
				Database: getIntOrDefault("REDIS_DB", 0),
				URL:      getEnvOrDefault("REDIS_URL", "redis://localhost:6379/0"),
			},
			SQLite: SQLiteConfig{
				Path: getEnvOrDefault("STOCK_DB", "configs/stocks.db"),
			},
		},
		APIs: APIConfig{
			StockAPI: StockAPIConfig{
				BaseURL:    getEnvOrDefault("API_BASE_URL", "https://uatdev.indiratrade.com/companies-details"),
				Timeout:    getDurationOrDefault("STOCK_API_TIMEOUT", 30*time.Second),
				RetryCount: getIntOrDefault("STOCK_API_RETRY_COUNT", 3),
				RateLimit:  getIntOrDefault("STOCK_API_RATE_LIMIT", 60),
			},
			HistoricalAPI: HistoricalAPIConfig{
				BaseURL:    getEnvOrDefault("HISTORICAL_API_URL", "https://trading.indiratrade.com:3000"),
				Timeout:    getDurationOrDefault("HISTORICAL_API_TIMEOUT", 30*time.Second),
				RetryCount: getIntOrDefault("HISTORICAL_API_RETRY_COUNT", 3),
			},
		},
		App: AppConfig{
			Environment:     getEnvOrDefault("ENVIRONMENT", "development"),
			Debug:           getBoolOrDefault("DEBUG", false),
			TestMode:        getBoolOrDefault("TEST_MODE", false),
			PythonScript:    getEnvOrDefault("PYTHON_SCRIPT", "scripts/b2c_bridge.py"),
			MarketOpen:      getDurationOrDefault("MARKET_OPEN", 9*time.Hour+15*time.Minute),
			MarketClose:     getDurationOrDefault("MARKET_CLOSE", 15*time.Hour+30*time.Minute),
			LateTolerance:   getDurationOrDefault("LATE_TOLERANCE", 2*time.Minute),
			CacheExpiration: getDurationOrDefault("CACHE_EXPIRATION", 7*24*time.Hour),
		},
		Logging: LoggingConfig{
			Level:      getEnvOrDefault("LOG_LEVEL", "info"),
			Format:     getEnvOrDefault("LOG_FORMAT", "json"),
			Output:     getEnvOrDefault("LOG_OUTPUT", "stdout"),
			MaxSize:    getIntOrDefault("LOG_MAX_SIZE", 100),
			MaxBackups: getIntOrDefault("LOG_MAX_BACKUPS", 3),
			MaxAge:     getIntOrDefault("LOG_MAX_AGE", 28),
		},
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return config, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate server configuration
	if c.Server.Port == "" {
		return fmt.Errorf("server port is required")
	}

	// Validate database configuration
	if c.Database.TimescaleDB.URL == "" {
		return fmt.Errorf("TimescaleDB URL is required")
	}

	if c.Database.Redis.URL == "" {
		return fmt.Errorf("Redis URL is required")
	}

	if c.Database.SQLite.Path == "" {
		return fmt.Errorf("SQLite path is required")
	}

	// Validate API configuration
	if c.APIs.StockAPI.BaseURL == "" {
		return fmt.Errorf("Stock API base URL is required")
	}

	if c.APIs.HistoricalAPI.BaseURL == "" {
		return fmt.Errorf("Historical API base URL is required")
	}

	// Validate app configuration
	if c.App.PythonScript == "" {
		return fmt.Errorf("Python script path is required")
	}

	// Validate market hours
	if c.App.MarketOpen >= c.App.MarketClose {
		return fmt.Errorf("market open time must be before market close time")
	}

	return nil
}

// IsProduction returns true if running in production environment
func (c *Config) IsProduction() bool {
	return strings.ToLower(c.App.Environment) == "production"
}

// IsDevelopment returns true if running in development environment
func (c *Config) IsDevelopment() bool {
	return strings.ToLower(c.App.Environment) == "development"
}

// GetTimescaleConnectionString returns the TimescaleDB connection string
func (c *Config) GetTimescaleConnectionString() string {
	if c.Database.TimescaleDB.URL != "" {
		return c.Database.TimescaleDB.URL
	}

	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.Database.TimescaleDB.Username,
		c.Database.TimescaleDB.Password,
		c.Database.TimescaleDB.Host,
		c.Database.TimescaleDB.Port,
		c.Database.TimescaleDB.Database,
		c.Database.TimescaleDB.SSLMode,
	)
}

// GetRedisConnectionString returns the Redis connection string
func (c *Config) GetRedisConnectionString() string {
	if c.Database.Redis.URL != "" {
		return c.Database.Redis.URL
	}

	if c.Database.Redis.Password != "" {
		return fmt.Sprintf("redis://:%s@%s:%d/%d",
			c.Database.Redis.Password,
			c.Database.Redis.Host,
			c.Database.Redis.Port,
			c.Database.Redis.Database,
		)
	}

	return fmt.Sprintf("redis://%s:%d/%d",
		c.Database.Redis.Host,
		c.Database.Redis.Port,
		c.Database.Redis.Database,
	)
}

// Helper functions for environment variable parsing

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getBoolOrDefault(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func getDurationOrDefault(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
