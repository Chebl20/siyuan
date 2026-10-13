package multiuser

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL         string
	BaseDomain          string
	AppHost             string
	PublicPort          string
	CookieSecret        []byte
	CookieSecure        bool
	WorkspaceRoot       string
	DockerWorkspaceRoot string
	SiYuanImage         string
	DockerNetwork       string
	SignupEnabled       bool
	SMTPHost            string
	SMTPPort            int
	SMTPUsername        string
	SMTPPassword        string
	SMTPFrom            string
	PublicScheme        string
}

func LoadConfig() (Config, error) {
	config := Config{
		DatabaseURL:         strings.TrimSpace(os.Getenv("DATABASE_URL")),
		BaseDomain:          strings.ToLower(strings.TrimSpace(os.Getenv("BASE_DOMAIN"))),
		AppHost:             strings.ToLower(strings.TrimSpace(os.Getenv("APP_HOST"))),
		PublicPort:          strings.TrimSpace(os.Getenv("PUBLIC_PORT")),
		CookieSecret:        []byte(os.Getenv("COOKIE_SECRET")),
		CookieSecure:        envBool("COOKIE_SECURE", true),
		WorkspaceRoot:       strings.TrimSpace(os.Getenv("WORKSPACE_ROOT")),
		DockerWorkspaceRoot: strings.TrimSpace(os.Getenv("DOCKER_WORKSPACE_ROOT")),
		SiYuanImage:         strings.TrimSpace(os.Getenv("SIYUAN_IMAGE")),
		DockerNetwork:       strings.TrimSpace(os.Getenv("MULTIUSER_NETWORK")),
		SignupEnabled:       envBool("SIGNUP_ENABLED", true),
		SMTPHost:            strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:            envInt("SMTP_PORT", 587),
		SMTPUsername:        strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword:        os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:            strings.TrimSpace(os.Getenv("SMTP_FROM")),
	}
	if !strings.HasPrefix(config.DatabaseURL, "postgres") {
		return Config{}, errors.New("DATABASE_URL must be a PostgreSQL URL")
	}
	if config.BaseDomain == "" || strings.Contains(config.BaseDomain, "/") || strings.Contains(config.BaseDomain, ":") {
		return Config{}, errors.New("BASE_DOMAIN must be a domain without a scheme or port")
	}
	if config.AppHost == "" || !strings.HasSuffix(config.AppHost, "."+config.BaseDomain) {
		return Config{}, errors.New("APP_HOST must be a subdomain of BASE_DOMAIN")
	}
	if config.PublicPort != "" {
		port, err := strconv.Atoi(config.PublicPort)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, errors.New("PUBLIC_PORT must be empty or a valid port")
		}
	}
	if len(config.CookieSecret) < 32 {
		return Config{}, errors.New("COOKIE_SECRET must contain at least 32 bytes")
	}
	if config.WorkspaceRoot == "" || !strings.HasPrefix(config.WorkspaceRoot, "/") {
		return Config{}, errors.New("WORKSPACE_ROOT must be an absolute path inside the gateway container")
	}
	if !isDockerHostPath(config.DockerWorkspaceRoot) {
		return Config{}, errors.New("DOCKER_WORKSPACE_ROOT must be an absolute host path")
	}
	if config.SiYuanImage == "" || config.DockerNetwork == "" {
		return Config{}, errors.New("SIYUAN_IMAGE and MULTIUSER_NETWORK are required")
	}
	if config.SMTPHost != "" && (config.SMTPFrom == "" || config.SMTPPort < 1 || config.SMTPPort > 65535) {
		return Config{}, errors.New("SMTP_FROM and a valid SMTP_PORT are required when SMTP_HOST is set")
	}
	config.PublicScheme = "https"
	if !config.CookieSecure {
		config.PublicScheme = "http"
	}
	return config, nil
}

var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func isDockerHostPath(path string) bool {
	return strings.HasPrefix(path, "/") || windowsAbsolutePath.MatchString(path)
}

func (c Config) portalURL(path string) string {
	return (&url.URL{Scheme: c.PublicScheme, Host: c.publicAuthority(c.AppHost), Path: path}).String()
}

func (c Config) workspaceURL(slug string) string {
	return (&url.URL{Scheme: c.PublicScheme, Host: c.publicAuthority(slug + "." + c.BaseDomain)}).String()
}

func (c Config) publicAuthority(host string) string {
	if c.PublicPort == "" {
		return host
	}
	return net.JoinHostPort(host, c.PublicPort)
}

// sessionCookieDomain returns the Domain attribute for portal session cookies.
// localhost is a public suffix, so Domain=localhost is rejected; .localhost shares across *.localhost.
func (c Config) sessionCookieDomain() string {
	if c.BaseDomain == "localhost" {
		return ".localhost"
	}
	return c.BaseDomain
}

func (c Config) csrfToken(sessionToken string) string {
	mac := hmac.New(sha256.New, c.CookieSecret)
	_, _ = mac.Write([]byte(sessionToken))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
