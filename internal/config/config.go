package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	DB         DBConfig         `mapstructure:"db"`
	Redis      RedisConfig      `mapstructure:"redis"`
	Internal   InternalConfig   `mapstructure:"internal"`
	RequestLog RequestLogConfig `mapstructure:"request_log"`
}

type RequestLogConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// TierLimit 定义单个订阅等级的限流参数。
// RPM/TPM 为用户级 60s 滚动窗口上限；ModelRPM 为单用户对单模型的 60s 请求上限。
// 值为 0 表示回落到 ServerConfig 中的全局默认（RPM/TPM）或不限制（ModelRPM）。
type TierLimit struct {
	RPM      int64 `mapstructure:"rpm"`
	TPM      int64 `mapstructure:"tpm"`
	ModelRPM int64 `mapstructure:"model_rpm"` // 单用户单模型 RPM，0 = 不限制
}

type ServerConfig struct {
	Port int `mapstructure:"port"`
	// RPMLimit / TPMLimit 为全局兜底默认值（tier 未配置时回落到此值）。
	RPMLimit int64 `mapstructure:"rpm_limit"`
	TPMLimit int64 `mapstructure:"tpm_limit"`
	// TierLimits 按订阅等级 (tier_level) 差异化限流。
	// key = tier_level 整数字符串（"0", "1", "2"），value = 该等级的限流参数。
	TierLimits map[string]TierLimit `mapstructure:"tier_limits"`
}

// ResolveTierLimit 返回指定 tier_level 的有效限流参数。
// 未配置的字段回落至全局默认（RPM/TPM），ModelRPM 未配则返回 0（不限制）。
func (s *ServerConfig) ResolveTierLimit(tierLevel int32) TierLimit {
	key := fmt.Sprintf("%d", tierLevel)
	tl, ok := s.TierLimits[key]
	if !ok {
		return TierLimit{RPM: s.RPMLimit, TPM: s.TPMLimit, ModelRPM: 0}
	}
	if tl.RPM <= 0 {
		tl.RPM = s.RPMLimit
	}
	if tl.TPM <= 0 {
		tl.TPM = s.TPMLimit
	}
	// ModelRPM 0 = 不限制，无需回落
	return tl
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
	viper.SetDefault("request_log.enabled", false)

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
