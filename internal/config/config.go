package config

import (
	"strings"

	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/config"
	"github.com/spf13/viper"
)

type ProbeConfig struct {
	appEnv          string
	port            string
	service         string
	region          string
	projectID       string
	spannerDB       string
	subscription    string
	dlqSubscription string
}

func LoadProbeConfig() (*ProbeConfig, error) {

	v := viper.New()
	v.SetDefault("PORT", "8080")
	v.SetDefault("APP_ENV", "local")
	v.SetDefault("SERVICE", "probe")
	v.SetDefault("REGION", "asia-south1")
	v.SetDefault("PROJECT_ID", "apex-494315")
	v.SetDefault("SPANNER_DATABASE", "projects/test-project/instances/test-instance/databases/test-database")
	v.SetDefault("SUBSCRIPTION", "projects/test-project/subscriptions/test-subscription")
	// Empty disables the DLQ handler goroutine.
	v.SetDefault("DLQ_SUBSCRIPTION", "")

	if err := config.LoadConfig(v, "config"); err != nil {
		return nil, err
	}

	cfg := &ProbeConfig{
		appEnv:          v.GetString("APP_ENV"),
		port:            normalizePort(v.GetString("PORT")),
		service:         v.GetString("SERVICE"),
		region:          v.GetString("REGION"),
		projectID:       v.GetString("PROJECT_ID"),
		spannerDB:       v.GetString("SPANNER_DATABASE"),
		subscription:    v.GetString("SUBSCRIPTION"),
		dlqSubscription: v.GetString("DLQ_SUBSCRIPTION"),
	}

	return cfg, nil
}

func normalizePort(port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		return ":8080"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}

func (c *ProbeConfig) AppEnv() string    { return c.appEnv }
func (c *ProbeConfig) Port() string      { return c.port }
func (c *ProbeConfig) Service() string   { return c.service }
func (c *ProbeConfig) Region() string    { return c.region }
func (c *ProbeConfig) ProjectID() string { return c.projectID }
func (c *ProbeConfig) SpannerDatabase() string {
	return c.spannerDB
}
func (c *ProbeConfig) Subscription() string    { return c.subscription }
func (c *ProbeConfig) DLQSubscription() string { return c.dlqSubscription }

