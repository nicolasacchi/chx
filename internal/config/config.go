// Package config loads multi-profile credentials from ~/.config/chx/config.toml
// and resolves them per-field (flag > env > profile block > default_profile).
//
// chx has TWO orthogonal API surfaces with distinct credentials:
//
//	Surface A (SQL endpoint at port 8443): host + port + sql_user + sql_password + database
//	Surface B (Cloud Mgmt API at api.clickhouse.cloud/v1): cloud_organization_id + cloud_key_id + cloud_key_secret
//
// Both live in the same [profiles.<name>] block; either surface is optional
// (a profile can be SQL-only or Cloud-only, but most have both).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Env var names (CLI-flag > env > config-file precedence; each field independent).
const (
	EnvProfile = "CHX_PROFILE"

	// Surface A — SQL
	EnvHost     = "CHX_HOST"
	EnvPort     = "CHX_PORT"
	EnvUser     = "CHX_USER"
	EnvPassword = "CHX_PASSWORD"
	EnvDatabase = "CHX_DATABASE"

	// Surface B — Cloud Mgmt API
	EnvCloudOrgID     = "CHX_CLOUD_ORG_ID"
	EnvCloudKeyID     = "CHX_CLOUD_KEY_ID"
	EnvCloudKeySecret = "CHX_CLOUD_KEY_SECRET"
)

// Credentials is the resolved set used by the SQL + Cloud clients.
type Credentials struct {
	// Surface A — SQL endpoint
	Host     string
	Port     int
	Secure   bool
	SQLUser  string
	SQLPass  string
	Database string

	// Surface B — Cloud Mgmt API (any of these empty disables that surface)
	CloudOrgID     string
	CloudKeyID     string
	CloudKeySecret string

	// Defaults pulled from profile (overridable per-call)
	Readonly bool // → readonly=2 URL param
}

// HasSQL reports whether the SQL endpoint is configured.
func (c *Credentials) HasSQL() bool { return c.Host != "" && c.SQLUser != "" }

// HasCloud reports whether the Cloud Mgmt API is configured.
func (c *Credentials) HasCloud() bool {
	return c.CloudOrgID != "" && c.CloudKeyID != "" && c.CloudKeySecret != ""
}

// Profile is one entry in config.toml under [profiles.<name>].
type Profile struct {
	// Surface A — SQL
	Host           string `toml:"host"`
	Port           int    `toml:"port"`
	Secure         bool   `toml:"secure"`
	SQLUser        string `toml:"sql_user"`
	SQLPassword    string `toml:"sql_password"`
	SQLPasswordEnv string `toml:"sql_password_env"` // alt: read password from a named env var
	Database       string `toml:"database"`

	// Surface B — Cloud Mgmt API
	CloudOrganizationID string `toml:"cloud_organization_id"`
	CloudKeyID          string `toml:"cloud_key_id"`
	CloudKeySecret     string `toml:"cloud_key_secret"`
	CloudKeySecretEnv  string `toml:"cloud_key_secret_env"` // alt: read secret from a named env var

	// Defaults
	Readonly bool `toml:"readonly"`
}

// Config is the on-disk shape of ~/.config/chx/config.toml.
type Config struct {
	DefaultProfile string             `toml:"default_profile"`
	Profiles       map[string]Profile `toml:"profiles"`
}

// ErrNoCredentials is returned when neither flag, env, nor config provides any credentials.
var ErrNoCredentials = errors.New("no credentials: use flags, CHX_* env vars, or run 'chx config add <name>'")

// Resolver collects the CLI flag values; LoadCredentials uses it to override config + env.
type Resolver struct {
	Profile string

	// Surface A
	Host     string
	Port     int
	User     string
	Password string
	Database string

	// Surface B
	CloudOrgID     string
	CloudKeyID     string
	CloudKeySecret string
}

// LoadCredentials resolves credentials from flag > env > profile > default_profile.
// Each field resolves independently, so e.g. CHX_PASSWORD env can pair with config.host.
func LoadCredentials(r Resolver) (*Credentials, error) {
	creds := &Credentials{}

	cfg, _ := Load() // missing config is OK; we'll fall through to flag/env

	// Pick a profile block to read defaults from
	var prof *Profile
	if cfg != nil {
		name := r.Profile
		if name == "" {
			name = os.Getenv(EnvProfile)
		}
		if name == "" {
			name = cfg.DefaultProfile
		}
		if name != "" {
			if p, ok := cfg.Profiles[name]; ok {
				prof = &p
			} else if r.Profile != "" {
				return nil, fmt.Errorf("profile %q not found in config; available: %v", r.Profile, profileNames(cfg.Profiles))
			}
		}
	}

	// --- Surface A: SQL endpoint ---
	creds.Host = firstNonEmpty(r.Host, os.Getenv(EnvHost), profileField(prof, func(p *Profile) string { return p.Host }))
	creds.Port = firstNonZero(r.Port, atoiOrZero(os.Getenv(EnvPort)), profileInt(prof, func(p *Profile) int { return p.Port }))
	creds.SQLUser = firstNonEmpty(r.User, os.Getenv(EnvUser), profileField(prof, func(p *Profile) string { return p.SQLUser }))
	creds.SQLPass = resolvePassword(r.Password, os.Getenv(EnvPassword), prof)
	creds.Database = firstNonEmpty(r.Database, os.Getenv(EnvDatabase), profileField(prof, func(p *Profile) string { return p.Database }))

	if creds.Port == 0 {
		creds.Port = 8443
	}
	if prof != nil {
		creds.Secure = prof.Secure || creds.Port == 8443
		creds.Readonly = prof.Readonly
	} else {
		creds.Secure = creds.Port == 8443
		creds.Readonly = true // safe default
	}

	// --- Surface B: Cloud Mgmt API ---
	creds.CloudOrgID = firstNonEmpty(r.CloudOrgID, os.Getenv(EnvCloudOrgID), profileField(prof, func(p *Profile) string { return p.CloudOrganizationID }))
	creds.CloudKeyID = firstNonEmpty(r.CloudKeyID, os.Getenv(EnvCloudKeyID), profileField(prof, func(p *Profile) string { return p.CloudKeyID }))
	creds.CloudKeySecret = resolveCloudSecret(r.CloudKeySecret, os.Getenv(EnvCloudKeySecret), prof)

	// At least one surface must be configured
	if !creds.HasSQL() && !creds.HasCloud() {
		return nil, ErrNoCredentials
	}

	return creds, nil
}

// resolvePassword: flag > CHX_PASSWORD env > profile.SQLPassword > profile.SQLPasswordEnv-resolved.
func resolvePassword(flag, env string, prof *Profile) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	if prof == nil {
		return ""
	}
	if prof.SQLPassword != "" {
		return prof.SQLPassword
	}
	if prof.SQLPasswordEnv != "" {
		return os.Getenv(prof.SQLPasswordEnv)
	}
	return ""
}

// resolveCloudSecret: flag > CHX_CLOUD_KEY_SECRET env > profile.CloudKeySecret > profile.CloudKeySecretEnv-resolved.
func resolveCloudSecret(flag, env string, prof *Profile) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	if prof == nil {
		return ""
	}
	if prof.CloudKeySecret != "" {
		return prof.CloudKeySecret
	}
	if prof.CloudKeySecretEnv != "" {
		return os.Getenv(prof.CloudKeySecretEnv)
	}
	return ""
}

// Load reads ~/.config/chx/config.toml. Missing file returns (nil, nil).
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	return &cfg, nil
}

// Save writes the config to ~/.config/chx/config.toml (creating dir if needed, mode 0600).
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	return enc.Encode(cfg)
}

// Path returns the config file path: $XDG_CONFIG_HOME/chx/config.toml or ~/.config/chx/config.toml.
func Path() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "chx", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "chx", "config.toml"), nil
}

// --- helpers ---

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(n ...int) int {
	for _, v := range n {
		if v != 0 {
			return v
		}
	}
	return 0
}

func profileField(p *Profile, get func(*Profile) string) string {
	if p == nil {
		return ""
	}
	return get(p)
}

func profileInt(p *Profile, get func(*Profile) int) int {
	if p == nil {
		return 0
	}
	return get(p)
}

func profileNames(m map[string]Profile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func atoiOrZero(s string) int {
	if s == "" {
		return 0
	}
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0
	}
	return n
}
