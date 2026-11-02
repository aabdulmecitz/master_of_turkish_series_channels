package main

import (
	"os"
	"strconv"
	"strings"
)

type FleetConfig struct {
	DatabaseURL       string
	RedisURL          string
	BotToken          string
	BotUsername       string
	LocalBotAPIServer string
	WebServerPort     string
	WebPlayerBaseURL  string
	WebPlayerDir      string
	MediaRoot         string
	VIPPriceStars     int
	VIPDurationDays   int
	RequiredChannels  []string
}

func LoadConfig() FleetConfig {
	reqRaw := getEnv("REQUIRED_CHANNELS", "@TurDizi_AnaHub,@Zimorodok_Official_RU")
	var channels []string
	for _, ch := range strings.Split(reqRaw, ",") {
		trimmed := strings.TrimSpace(ch)
		if trimmed != "" {
			if !strings.HasPrefix(trimmed, "@") {
				trimmed = "@" + trimmed
			}
			channels = append(channels, trimmed)
		}
	}

	vipStars, err := strconv.Atoi(getEnv("VIP_PRICE_STARS", "50"))
	if err != nil || vipStars <= 0 {
		vipStars = 50
	}

	vipDays, err := strconv.Atoi(getEnv("VIP_DURATION_DAYS", "30"))
	if err != nil || vipDays <= 0 {
		vipDays = 30
	}

	return FleetConfig{
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://fleetadmin:supersecretpassword@localhost:5432/telefleet?sslmode=disable"),
		RedisURL:          getEnv("REDIS_URL", "localhost:6379"),
		BotToken:          getEnv("TELEGRAM_BOT_TOKEN", ""),
		BotUsername:       strings.TrimPrefix(getEnv("TELEGRAM_BOT_USERNAME", "TurDiziMasterBot"), "@"),
		LocalBotAPIServer: strings.TrimRight(getEnv("LOCAL_BOT_API_URL", "http://telegram-bot-api:8081"), "/"),
		WebServerPort:     getEnv("WEB_SERVER_PORT", "8080"),
		WebPlayerBaseURL:  strings.TrimRight(getEnv("WEB_PLAYER_URL", "http://localhost:8080/watch"), "/"),
		WebPlayerDir:      getEnv("WEB_PLAYER_DIR", "./web-player"),
		MediaRoot:         getEnv("MEDIA_ROOT", "/var/lib/telegram-bot-api/media"),
		VIPPriceStars:     vipStars,
		VIPDurationDays:   vipDays,
		RequiredChannels:  channels,
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}

