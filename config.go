package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	JMAP              JMAPConfig   `json:"jmap"`
	NATS              NATSConfig   `json:"nats"`
	Stream            StreamConfig `json:"stream"`
	Parts             PartsConfig  `json:"parts"`
	Cursor            CursorConfig `json:"cursor"`
	JetStreamReplicas int          `json:"jetstream_replicas"`
	BackfillLimit     uint64       `json:"backfill_limit"`
}

type JMAPConfig struct {
	SessionURL string `json:"session_url"`
	TokenFile  string `json:"token_file"`
	AccountID  string `json:"account_id"`
}

type NATSConfig struct {
	URL          string `json:"url"`
	TokenFile    string `json:"token_file"`
	User         string `json:"user"`
	UserFile     string `json:"user_file"`
	PasswordFile string `json:"password_file"`
	CredsFile    string `json:"creds_file"`
	NkeySeedFile string `json:"nkey_seed_file"`
}

type StreamConfig struct {
	Name              string   `json:"name"`
	SubjectPrefix     string   `json:"subject_prefix"`
	MaxAge            Duration `json:"max_age"`
	MaxBytes          Bytes    `json:"max_bytes"`
	DedupWindow       Duration `json:"dedup_window"`
	ExternallyManaged bool     `json:"externally_managed"`
}

type PartsConfig struct {
	Bucket     string `json:"bucket"`
	MaxBytes   Bytes  `json:"max_bytes"`
	MaxPerPart Bytes  `json:"max_per_part"`
}

type CursorConfig struct {
	Bucket string `json:"bucket"`
}

func defaultConfig() Config {
	return Config{
		NATS: NATSConfig{
			URL: "nats://localhost:4222",
		},
		Stream: StreamConfig{
			Name:          "JMAP_EMAILS",
			SubjectPrefix: "jmap.email",
			MaxAge:        Duration(7 * 24 * time.Hour),
			MaxBytes:      64 * MiB,
			DedupWindow:   Duration(24 * time.Hour),
		},
		Parts: PartsConfig{
			Bucket:     "email-parts",
			MaxBytes:   960 * MiB,
			MaxPerPart: 25 * MiB,
		},
		JetStreamReplicas: 1,
		BackfillLimit:     100,
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	applyDerivedDefaults(&cfg)
	if err := cfg.validate(); err != nil {
		return cfg, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func TemplateConfig() Config {
	cfg := defaultConfig()
	cfg.JMAP.SessionURL = "https://api.fastmail.com/jmap/session"
	cfg.JMAP.TokenFile = "/etc/jmap2nats/token"
	applyDerivedDefaults(&cfg)
	return cfg
}

func applyDerivedDefaults(cfg *Config) {
	if cfg.Cursor.Bucket == "" {
		cfg.Cursor.Bucket = cfg.Stream.Name + "_CURSOR"
	}
}

func (c Config) validate() error {
	if c.JMAP.SessionURL == "" {
		return fmt.Errorf("jmap.session_url is required")
	}
	if c.JMAP.TokenFile == "" {
		return fmt.Errorf("jmap.token_file is required")
	}
	if c.NATS.URL == "" {
		return fmt.Errorf("nats.url is required")
	}
	if c.NATS.User != "" && c.NATS.UserFile != "" {
		return fmt.Errorf("nats: set at most one of user or user_file")
	}
	userSet := c.NATS.User != "" || c.NATS.UserFile != ""
	if userSet != (c.NATS.PasswordFile != "") {
		return fmt.Errorf("nats.user/user_file and nats.password_file must be set together")
	}
	methods := 0
	for _, on := range []bool{c.NATS.TokenFile != "", userSet, c.NATS.CredsFile != "", c.NATS.NkeySeedFile != ""} {
		if on {
			methods++
		}
	}
	if methods > 1 {
		return fmt.Errorf("nats: choose at most one of token_file, user/password_file, creds_file, or nkey_seed_file")
	}
	if c.Stream.Name == "" || c.Stream.SubjectPrefix == "" {
		return fmt.Errorf("stream.name and stream.subject_prefix are required")
	}
	if c.Stream.MaxBytes <= 0 {
		return fmt.Errorf("stream.max_bytes must be positive")
	}
	if c.Parts.Bucket == "" {
		return fmt.Errorf("parts.bucket is required")
	}
	if c.Cursor.Bucket == "" {
		return fmt.Errorf("cursor.bucket is required")
	}
	if c.Parts.MaxBytes <= 0 || c.Parts.MaxPerPart <= 0 {
		return fmt.Errorf("parts.max_bytes and parts.max_per_part must be positive")
	}
	if c.Stream.MaxAge <= 0 {
		return fmt.Errorf("stream.max_age must be positive")
	}
	if c.JetStreamReplicas < 1 {
		return fmt.Errorf("jetstream_replicas must be at least 1")
	}
	if c.BackfillLimit == 0 {
		return fmt.Errorf("backfill_limit must be positive")
	}
	return nil
}

// readSecretFile reads a secret from a file, trimming surrounding whitespace and
// rejecting empty contents. Shared by the JMAP token and the NATS password.
func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return s, nil
}

type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d Duration) Duration() time.Duration { return time.Duration(d) }

type Bytes int64

const (
	KiB Bytes = 1024
	MiB       = 1024 * KiB
	GiB       = 1024 * MiB
)

func (b *Bytes) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		v, err := parseByteSize(s)
		if err != nil {
			return err
		}
		*b = v
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*b = Bytes(n)
	return nil
}

func (b Bytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(formatByteSize(b))
}

func (b Bytes) Int64() int64 { return int64(b) }

func parseByteSize(s string) (Bytes, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	suffixes := []struct {
		s string
		m Bytes
	}{
		{"GiB", GiB}, {"MiB", MiB}, {"KiB", KiB},
		{"GB", 1_000_000_000}, {"MB", 1_000_000}, {"KB", 1_000},
		{"B", 1},
	}
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf.s) {
			num := strings.TrimSpace(strings.TrimSuffix(s, suf.s))
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q: %w", s, err)
			}
			return Bytes(f * float64(suf.m)), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q (expected suffix B/KiB/MiB/GiB)", s)
	}
	return Bytes(n), nil
}

func formatByteSize(b Bytes) string {
	switch {
	case b >= GiB && b%GiB == 0:
		return fmt.Sprintf("%dGiB", b/GiB)
	case b >= MiB && b%MiB == 0:
		return fmt.Sprintf("%dMiB", b/MiB)
	case b >= KiB && b%KiB == 0:
		return fmt.Sprintf("%dKiB", b/KiB)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
