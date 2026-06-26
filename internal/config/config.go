package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	DB       DBConfig       `mapstructure:"db"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Internal InternalConfig `mapstructure:"internal"`
}

type ServerConfig struct {
	Port int `mapstructure:"port"`
	// RPMLimit / TPMLimit 为每用户滚动 60s 窗口内的请求数 / Token 数上限。
	// TPM 按「prompt + max_tokens 预留」计，agent 场景应配较高值；<=0 时回落到内置默认。
	RPMLimit int64 `mapstructure:"rpm_limit"`
	TPMLimit int64 `mapstructure:"tpm_limit"`
}

type DBConfig struct {
	DSN string `mapstructure:"dsn"`
	// 连接池上下限。需 > Worker 并发 + 单请求并发查询峰值，且 ≤ Postgres max_connections。
	// <=0 时回落到内置默认（MaxConns=20, MinConns=2）。
	MaxConns int32 `mapstructure:"max_conns"`
	MinConns int32 `mapstructure:"min_conns"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type InternalConfig struct {
	Token string `mapstructure:"token"`
}

var AppConfig *Config

func LoadConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	// Allow overriding via environment variables (prefix: AINODE_)
	viper.SetEnvPrefix("AINODE")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Default values
	viper.SetDefault("server.port", 8080)
	// 限流默认值（调高以适配 agent 类高频/大上下文场景；可被 config 覆盖）
	viper.SetDefault("server.rpm_limit", 600)
	viper.SetDefault("server.tpm_limit", 2000000)
	viper.SetDefault("db.dsn", "postgres://user:pass@localhost:5432/ainode?sslmode=disable")
	viper.SetDefault("db.max_conns", 20)
	viper.SetDefault("db.min_conns", 2)
	viper.SetDefault("redis.addr", "localhost:6379")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("internal.token", "")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Fatalf("Error reading config file: %v", err)
		} else {
			log.Println("Config file not found, using environment variables and defaults")
		}
	}

	AppConfig = &Config{}
	if err := viper.Unmarshal(AppConfig); err != nil {
		log.Fatalf("Unable to decode into struct: %v", err)
	}

	log.Printf("Configuration loaded successfully. Port: %d", AppConfig.Server.Port)
}
