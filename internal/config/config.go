package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version   int            `yaml:"version"`
	Server    ServerConfig   `yaml:"server"`
	Defaults  DefaultsConfig `yaml:"defaults"`
	Logging   LoggingConfig  `yaml:"logging"`
	Routes    []Route        `yaml:"routes"`
	Scenarios []Scenario     `yaml:"scenarios"`
	Admin     AdminConfig    `yaml:"admin"`
	SourceDir string         `yaml:"-"`
}
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

type ServerConfig struct {
	HTTPListen      string   `yaml:"http_listen"`
	AdminListen     string   `yaml:"admin_listen"`
	FixturesDir     string   `yaml:"fixtures_dir"`
	ReadTimeout     Duration `yaml:"read_timeout"`
	WriteTimeout    Duration `yaml:"write_timeout"`
	IdleTimeout     Duration `yaml:"idle_timeout"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
	MaxBodyBytes    int64    `yaml:"max_body_bytes"`
}
type DefaultsConfig struct {
	UnmatchedStatus int  `yaml:"unmatched_status"`
	Strict          bool `yaml:"strict"`
}
type LoggingConfig struct {
	Level     string `yaml:"level"`
	Format    string `yaml:"format"`
	LogBodies bool   `yaml:"log_bodies"`
}
type AdminConfig struct {
	Enabled bool `yaml:"enabled"`
}
type Route struct {
	ID        string   `yaml:"id"`
	Protocol  string   `yaml:"protocol"`
	Path      string   `yaml:"path"`
	Methods   []string `yaml:"methods"`
	Scenarios []string `yaml:"scenarios"`
}
type Scenario struct {
	ID       string            `yaml:"id"`
	Priority int               `yaml:"priority"`
	Match    Match             `yaml:"match"`
	Actions  []Action          `yaml:"actions"`
	State    map[string]string `yaml:"state,omitempty"`
}
type Match struct {
	Headers    map[string]StringCondition `yaml:"headers,omitempty"`
	Query      map[string]StringCondition `yaml:"query,omitempty"`
	PathParams map[string]StringCondition `yaml:"path_params,omitempty"`
	JSON       map[string]ValueCondition  `yaml:"json,omitempty"`
	XML        map[string]ValueCondition  `yaml:"xml,omitempty"`
	BodySHA256 string                     `yaml:"body_sha256,omitempty"`
	BodySize   *int64                     `yaml:"body_size,omitempty"`
	BodyRegex  string                     `yaml:"body_regex,omitempty"`
	BodyEquals string                     `yaml:"body_equals,omitempty"`
}
type StringCondition struct {
	Equals       string   `yaml:"equals,omitempty"`
	NotEquals    string   `yaml:"not_equals,omitempty"`
	Contains     string   `yaml:"contains,omitempty"`
	Exists       *bool    `yaml:"exists,omitempty"`
	Regex        string   `yaml:"regex,omitempty"`
	OneOf        []string `yaml:"one_of,omitempty"`
	EqualsSet    bool     `yaml:"-"`
	NotEqualsSet bool     `yaml:"-"`
}

func (c *StringCondition) UnmarshalYAML(node *yaml.Node) error {
	type plain StringCondition
	var value plain
	if err := node.Decode(&value); err != nil {
		return err
	}
	*c = StringCondition(value)
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			switch node.Content[i].Value {
			case "equals":
				c.EqualsSet = true
			case "not_equals":
				c.NotEqualsSet = true
			}
		}
	}
	return nil
}

type ValueCondition struct {
	Equals       any      `yaml:"equals,omitempty"`
	NotEquals    any      `yaml:"not_equals,omitempty"`
	Exists       *bool    `yaml:"exists,omitempty"`
	Type         string   `yaml:"type,omitempty"`
	Contains     any      `yaml:"contains,omitempty"`
	Regex        string   `yaml:"regex,omitempty"`
	GreaterThan  *float64 `yaml:"gt,omitempty"`
	GreaterEqual *float64 `yaml:"gte,omitempty"`
	LessThan     *float64 `yaml:"lt,omitempty"`
	LessEqual    *float64 `yaml:"lte,omitempty"`
	EqualsSet    bool     `yaml:"-"`
	NotEqualsSet bool     `yaml:"-"`
}

func (c *ValueCondition) UnmarshalYAML(node *yaml.Node) error {
	type plain ValueCondition
	var value plain
	if err := node.Decode(&value); err != nil {
		return err
	}
	*c = ValueCondition(value)
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			switch node.Content[i].Value {
			case "equals":
				c.EqualsSet = true
			case "not_equals":
				c.NotEqualsSet = true
			}
		}
	}
	return nil
}

type Action struct {
	Type      string            `yaml:"type"`
	Status    int               `yaml:"status,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
	Body      Body              `yaml:"body,omitempty"`
	Responses []ResponseStep    `yaml:"responses,omitempty"`
	Cycle     bool              `yaml:"cycle,omitempty"`
	Delay     Duration          `yaml:"delay,omitempty"`
	Timeout   Duration          `yaml:"timeout,omitempty"`
	Async     bool              `yaml:"async,omitempty"`
	Message   Body              `yaml:"message,omitempty"`
	Code      int               `yaml:"code,omitempty"`
	Reason    string            `yaml:"reason,omitempty"`
	Key       string            `yaml:"key,omitempty"`
	Value     string            `yaml:"value,omitempty"`
}
type ResponseStep struct {
	Status  int               `yaml:"status,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Body    Body              `yaml:"body,omitempty"`
}
type Body struct {
	Type     string                    `yaml:"type,omitempty"`
	Value    any                       `yaml:"value,omitempty"`
	Path     string                    `yaml:"path,omitempty"`
	Template string                    `yaml:"template,omitempty"`
	Match    map[string]ValueCondition `yaml:"match,omitempty"`
}
type CLIOverrides struct {
	Listen, AdminListen, FixturesDir, LogLevel, LogFormat   string
	Strict                                                  bool
	NoStrict                                                bool
	MaxBodyBytes                                            int64
	ReadTimeout, WriteTimeout, IdleTimeout, ShutdownTimeout time.Duration
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		loaded, err := loadTOMLConfig(path, b)
		if err != nil {
			return nil, err
		}
		c = *loaded
	} else {
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	c.SourceDir = abs
	if c.Server.FixturesDir == "" {
		c.Server.FixturesDir = "fixtures"
	}
	if !filepath.IsAbs(c.Server.FixturesDir) {
		c.Server.FixturesDir = filepath.Join(abs, c.Server.FixturesDir)
	}
	return &c, nil
}
func (c *Config) ApplyCLI(o CLIOverrides) {
	if o.Listen != "" {
		c.Server.HTTPListen = o.Listen
	}
	if strings.EqualFold(o.AdminListen, "off") {
		c.Server.AdminListen = ""
		c.Admin.Enabled = false
	} else if o.AdminListen != "" {
		c.Server.AdminListen = o.AdminListen
		c.Admin.Enabled = true
	}
	if o.FixturesDir != "" {
		c.Server.FixturesDir = o.FixturesDir
	}
	if o.LogLevel != "" {
		c.Logging.Level = o.LogLevel
	}
	if o.LogFormat != "" {
		c.Logging.Format = o.LogFormat
	}
	if o.Strict {
		c.Defaults.Strict = true
	}
	if o.NoStrict {
		c.Defaults.Strict = false
	}
	if o.MaxBodyBytes > 0 {
		c.Server.MaxBodyBytes = o.MaxBodyBytes
	}
	if o.ReadTimeout > 0 {
		c.Server.ReadTimeout = Duration(o.ReadTimeout)
	}
	if o.WriteTimeout > 0 {
		c.Server.WriteTimeout = Duration(o.WriteTimeout)
	}
	if o.IdleTimeout > 0 {
		c.Server.IdleTimeout = Duration(o.IdleTimeout)
	}
	if o.ShutdownTimeout > 0 {
		c.Server.ShutdownTimeout = Duration(o.ShutdownTimeout)
	}
}
func (c *Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d (expected 1)", c.Version)
	}
	if c.Server.HTTPListen == "" {
		return errors.New("server.http_listen is required")
	}
	if c.Server.MaxBodyBytes <= 0 {
		c.Server.MaxBodyBytes = 50 << 20
	}
	if c.Defaults.UnmatchedStatus == 0 {
		c.Defaults.UnmatchedStatus = 404
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "text"
	}
	if c.Logging.Format != "text" && c.Logging.Format != "json" {
		return fmt.Errorf("logging.format must be text or json")
	}
	routes := map[string]bool{}
	scenarios := map[string]Scenario{}
	for _, s := range c.Scenarios {
		if s.ID == "" {
			return errors.New("scenario id cannot be empty")
		}
		if _, ok := scenarios[s.ID]; ok {
			return fmt.Errorf("duplicate scenario id %q", s.ID)
		}
		scenarios[s.ID] = s
		for _, a := range s.Actions {
			if a.Type == "sequence" && len(a.Responses) == 0 && a.Status == 0 && a.Body.Type == "" && a.Body.Value == nil && a.Body.Template == "" {
				return fmt.Errorf("scenario %q: sequence action requires responses or a legacy status/body", s.ID)
			}
			switch a.Type {
			case "respond", "delay", "abort", "capture", "assert", "set_state", "sequence", "ws_expect", "ws_send", "ws_close":
			default:
				return fmt.Errorf("scenario %q: unsupported action type %q", s.ID, a.Type)
			}
		}
		for field, cond := range s.Match.Headers {
			if cond.Regex != "" {
				if _, err := regexp.Compile(cond.Regex); err != nil {
					return fmt.Errorf("scenario %q: invalid header %q regex: %w", s.ID, field, err)
				}
			}
		}
		for field, cond := range s.Match.Query {
			if cond.Regex != "" {
				if _, err := regexp.Compile(cond.Regex); err != nil {
					return fmt.Errorf("scenario %q: invalid query %q regex: %w", s.ID, field, err)
				}
			}
		}
		for field, cond := range s.Match.PathParams {
			if cond.Regex != "" {
				if _, err := regexp.Compile(cond.Regex); err != nil {
					return fmt.Errorf("scenario %q: invalid path parameter %q regex: %w", s.ID, field, err)
				}
			}
		}
		if s.Match.BodyRegex != "" {
			if _, err := regexp.Compile(s.Match.BodyRegex); err != nil {
				return fmt.Errorf("scenario %q: invalid body_regex: %w", s.ID, err)
			}
		}
		for path, cond := range s.Match.JSON {
			if cond.Regex != "" {
				if _, err := regexp.Compile(cond.Regex); err != nil {
					return fmt.Errorf("scenario %q: invalid JSON regex at %q: %w", s.ID, path, err)
				}
			}
		}
	}
	for _, r := range c.Routes {
		if r.ID == "" {
			return errors.New("route id cannot be empty")
		}
		if routes[r.ID] {
			return fmt.Errorf("duplicate route id %q", r.ID)
		}
		routes[r.ID] = true
		if r.Protocol != "http" && r.Protocol != "websocket" {
			return fmt.Errorf("route %q: protocol must be http or websocket", r.ID)
		}
		if r.Path == "" || !strings.HasPrefix(r.Path, "/") {
			return fmt.Errorf("route %q: path must start with /", r.ID)
		}
		for _, sid := range r.Scenarios {
			if _, ok := scenarios[sid]; !ok {
				return fmt.Errorf("route %q references unknown scenario %q", r.ID, sid)
			}
		}
	}
	if len(c.Routes) == 0 {
		return errors.New("at least one route is required")
	}
	return nil
}
