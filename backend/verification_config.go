package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

func positiveDuration(name, fallback string) (time.Duration, error) {
	value := envDefault(name, fallback)
	d, err := time.ParseDuration(value)
	if err != nil || d < time.Second {
		return 0, fmt.Errorf("%s must be a duration of at least one second", name)
	}
	return d, nil
}
func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func emailConfig() (verificationConfig, smtpSender, error) {
	var cfg verificationConfig
	var sender smtpSender
	fail := func(err error) (verificationConfig, smtpSender, error) { return cfg, sender, err }
	var err error
	cfg.Secret, err = hex.DecodeString(os.Getenv("EMAIL_CODE_SECRET"))
	if err != nil || len(cfg.Secret) < 32 {
		return fail(fmt.Errorf("EMAIL_CODE_SECRET must contain at least 32 random bytes encoded as hex"))
	}
	if cfg.CodeTTL, err = positiveDuration("EMAIL_CODE_TTL", "10m"); err != nil {
		return fail(err)
	}
	if cfg.RegistrationTTL, err = positiveDuration("REGISTRATION_TTL", "30m"); err != nil {
		return fail(err)
	}
	if cfg.ResendInterval, err = positiveDuration("EMAIL_RESEND_INTERVAL", "60s"); err != nil {
		return fail(err)
	}
	cfg.MaxAttempts, err = strconv.Atoi(envDefault("EMAIL_MAX_ATTEMPTS", "5"))
	if err != nil || cfg.MaxAttempts < 1 || cfg.MaxAttempts > 65535 {
		return fail(fmt.Errorf("EMAIL_MAX_ATTEMPTS must be between 1 and 65535"))
	}
	cfg.MaxSends, err = strconv.Atoi(envDefault("EMAIL_MAX_SENDS_PER_SOURCE", "20"))
	if err != nil || cfg.MaxSends < 1 {
		return fail(fmt.Errorf("EMAIL_MAX_SENDS_PER_SOURCE must be positive"))
	}
	if cfg.SendWindow, err = positiveDuration("EMAIL_SEND_WINDOW", "1h"); err != nil {
		return fail(err)
	}
	if cfg.CleanupInterval, err = positiveDuration("EMAIL_CLEANUP_INTERVAL", "1h"); err != nil {
		return fail(err)
	}
	cfg.TrustLoopbackProxy, err = strconv.ParseBool(envDefault("TRUST_LOOPBACK_PROXY", "false"))
	if err != nil {
		return fail(fmt.Errorf("TRUST_LOOPBACK_PROXY must be true or false"))
	}
	cfg.Origin = envDefault("APP_ORIGIN", "http://localhost:5173")
	if !validOrigin(cfg.Origin) {
		return fail(fmt.Errorf("APP_ORIGIN must be an http(s) origin without a path"))
	}
	cfg.SecureCookie, err = strconv.ParseBool(envDefault("COOKIE_SECURE", "true"))
	if err != nil {
		return fail(fmt.Errorf("COOKIE_SECURE must be true or false"))
	}
	sender.Host = envDefault("SMTP_HOST", "127.0.0.1")
	sender.Port = envDefault("SMTP_PORT", "1025")
	sender.From = envDefault("SMTP_FROM", "noreply@mitemau.test")
	sender.Username = os.Getenv("SMTP_USERNAME")
	sender.Password = os.Getenv("SMTP_PASSWORD")
	sender.CodeTTL = cfg.CodeTTL
	sender.RequireTLS, err = strconv.ParseBool(envDefault("SMTP_REQUIRE_TLS", "true"))
	if err != nil {
		return fail(fmt.Errorf("SMTP_REQUIRE_TLS must be true or false"))
	}
	if !sender.RequireTLS {
		ip := net.ParseIP(sender.Host)
		if sender.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fail(fmt.Errorf("unencrypted SMTP is only allowed on loopback"))
		}
	}
	return cfg, sender, nil
}
