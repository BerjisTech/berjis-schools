package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/berjistech/berjis-ecosystem/schools/service/internal/config"
	"github.com/berjistech/berjis-ecosystem/schools/service/internal/db"
	"github.com/berjistech/berjis-ecosystem/schools/service/internal/migrate"
	"github.com/berjistech/berjis-ecosystem/schools/service/internal/server"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	conn, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Printf("warn: failed to connect to schools DB: %v", err)
	} else {
		runner := migrate.Runner{Dir: "./migrations"}
		if err := runner.Up(conn); err != nil {
			log.Printf("warn: migrations failed: %v", err)
		}
	}

	app := server.New(server.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		DB:               conn,
		CoreAPIBase:      cfg.CoreAPIBase,
		SMTPHost:         cfg.SMTPHost,
		SMTPPort:         cfg.SMTPPort,
		SMTPUsername:     cfg.SMTPUsername,
		SMTPPassword:     cfg.SMTPPassword,
		SMTPFrom:         cfg.SMTPFrom,
		SMSProvider:      cfg.SMSProvider,
		TwilioAccountSID: cfg.TwilioAccountSID,
		TwilioAuthToken:  cfg.TwilioAuthToken,
		TwilioFrom:       cfg.TwilioFrom,
	})
	addr := ":" + cfg.Port
	log.Printf("starting %s on %s (env=%s)", cfg.AppName, addr, cfg.Env)
	if err := app.Listen(addr); err != nil {
		log.Println("shutdown:", err)
		os.Exit(1)
	}
}
