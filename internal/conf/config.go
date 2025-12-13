package conf

import (
	"github.com/spf13/viper"
	"log"
)

type Config struct {
	Server     ServerConfig
	MongoDB    MongoConfig
	Cloudflare CloudflareConfig
}

type ServerConfig struct {
	Port string
}

type MongoConfig struct {
	URI      string
	Database string
}

type CloudflareConfig struct {
	APIToken string `mapstructure:"api_token"`
}

func LoadConfig() (*Config, error) {
	viper.AddConfigPath("./config") // 設定檔路徑
	viper.SetConfigName("config")   // 檔名
	viper.SetConfigType("yaml")     // 格式

	viper.AutomaticEnv() // 允許讀取環境變數

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	log.Println("設定檔讀取成功")
	return &cfg, nil
}
