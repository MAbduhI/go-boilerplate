package config

import "time"

// App holds the process-wide settings that every service needs. Connector
// configs live beside their connector (postgres.Config, redis.Config, ...) so
// that shared/config stays free of driver imports.
type App struct {
	Name            string
	Env             string
	ServerPort      string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string
	LogFormat       string
	AllowOrigins    []string
}

// LoadApp reads App from the environment, applying defaults suited to local
// development.
func LoadApp() App {
	return App{
		Name:            String("APP_NAME", "go-boilerplate"),
		Env:             String("APP_ENV", "development"),
		ServerPort:      String("SERVER_PORT", "8080"),
		ReadTimeout:     Duration("SERVER_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:    Duration("SERVER_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:     Duration("SERVER_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout: Duration("SERVER_SHUTDOWN_TIMEOUT", 15*time.Second),
		LogLevel:        String("LOG_LEVEL", "info"),
		LogFormat:       String("LOG_FORMAT", "json"),
		AllowOrigins:    Strings("ALLOW_ORIGIN", []string{"*"}),
	}
}

// IsProduction reports whether the app runs in a production-like environment.
func (a App) IsProduction() bool { return a.Env == "production" || a.Env == "prod" }
