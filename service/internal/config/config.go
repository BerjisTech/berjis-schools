package config

import "os"

type Config struct {
	AppName          string
	Env              string
	Port             string
	DatabaseURL      string
	CoreAPIBase      string
	AllowedOrigins   string
	SMTPHost         string
	SMTPPort         string
	SMTPUsername     string
	SMTPPassword     string
	SMTPFrom         string
	SMSProvider      string
	TwilioAccountSID string
	TwilioAuthToken  string
	TwilioFrom       string
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Load() Config {
	return Config{
		AppName:          getenv("APP_NAME", "berjis-schools"),
		Env:              getenv("APP_ENV", "development"),
		Port:             getenv("PORT", "8088"),
		DatabaseURL:      getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5441/berjis_schools?sslmode=disable"),
		CoreAPIBase:      getenv("CORE_API_BASE", "http://localhost:8080"),
		AllowedOrigins:   getenv("ALLOWED_ORIGINS", "*"),
		SMTPHost:         getenv("SMTP_HOST", ""),
		SMTPPort:         getenv("SMTP_PORT", "587"),
		SMTPUsername:     getenv("SMTP_USERNAME", ""),
		SMTPPassword:     getenv("SMTP_PASSWORD", ""),
		SMTPFrom:         getenv("SMTP_FROM", "no-reply@schools.local"),
		SMSProvider:      getenv("SMS_PROVIDER", ""),
		TwilioAccountSID: getenv("TWILIO_ACCOUNT_SID", ""),
		TwilioAuthToken:  getenv("TWILIO_AUTH_TOKEN", ""),
		TwilioFrom:       getenv("TWILIO_FROM", ""),
	}
}
