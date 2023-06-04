package state

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"
	"go.interactor.dev/terradep"
)

// S3Stater is a [terradep.Stater] supporting backend type [S3Backend]
type S3Stater struct {
	cfg s3StaterCfg
}

// NewS3Stater returns configured instance of [S3Stater]
func NewS3Stater(opts ...S3StaterOpt) *S3Stater {
	cfg := &s3StaterCfg{}

	for _, opt := range opts {
		opt(cfg)
	}

	return &S3Stater{cfg: *cfg}
}

// S3StaterOpt is used by [NewS3Stater] to customize behaviour of created [S3Stater]
type S3StaterOpt func(cfg *s3StaterCfg)

// WithS3Region makes [S3Stater] add region to returned [terradep.State].
// When this option is used states with different regions won't be equal.
// When region is not specified it is treated as empty string
func WithS3Region() S3StaterOpt {
	return func(cfg *s3StaterCfg) {
		cfg.region = true
	}
}

// WithS3Encryption makes [S3Stater] add encryption to returned [terradep.State].
// When this option is used states with different encryption won't be equal.
// When encryption is not specified it is treated as false
func WithS3Encryption() S3StaterOpt {
	return func(cfg *s3StaterCfg) {
		cfg.encryption = true
	}
}

type s3StaterCfg struct {
	region     bool
	encryption bool
}

// S3Backend is key of Terraform backend type
const S3Backend = "s3"

// RemoteState implements [terradep.Stater]
func (s *S3Stater) RemoteState(backend string, stateCfg map[string]cty.Value) (terradep.State, error) {
	if backend != S3Backend {
		return nil, fmt.Errorf("supported backend type: %q, got: %q", S3Backend, backend)
	}

	cfg := s3Config{}
	for key, value := range stateCfg {
		switch key {
		case "bucket":
			cfg.Bucket = value.AsString()
		case "key":
			cfg.Key = value.AsString()
		case "region":
			cfg.Region = value.AsString()
		case "encrypt":
			cfg.Encrypt = value.RawEquals(cty.True)
		}
	}

	return s.urlFromConfig(cfg)
}

// BackendState implements [terradep.Stater]
func (s *S3Stater) BackendState(backend string, body hcl.Body) (terradep.State, error) {
	if backend != S3Backend {
		return nil, fmt.Errorf("supported backend type: %q, got: %q", S3Backend, backend)
	}

	cfg := &s3BackendConfig{}
	diags := gohcl.DecodeBody(body, nil, cfg)
	if diags.HasErrors() {
		return nil, fmt.Errorf("reading S3Backend state: %w", diags)
	}

	return s.urlFromConfig(s3Config(*cfg))
}

func (s *S3Stater) urlFromConfig(cfg s3Config) (s3StateURL, error) { //nolint:unparam
	u := url.URL{}
	u.Scheme = S3Backend
	u.Host = cfg.Bucket
	u.Path = cfg.Key
	q := u.Query()
	if s.cfg.region {
		q.Set("region", cfg.Region)
	}
	if s.cfg.encryption {
		q.Set("encrypt", strconv.FormatBool(cfg.Encrypt))
	}

	return s3StateURL(u.String()), nil
}

type s3Config struct {
	Bucket  string
	Key     string
	Region  string
	Encrypt bool
	Remain  *hcl.Body
}

type s3BackendConfig struct {
	Bucket  string `hcl:"bucket,attr"`
	Key     string `hcl:"key,attr"`
	Region  string `hcl:"region,attr"`
	Encrypt bool   `hcl:"encrypt,attr"`

	Remain *hcl.Body `hcl:"remain,optional"`
}

// S3State represents Terraform state stored in S3 bucket
type S3State struct {
	// Bucket is name of S3 bucket
	Bucket string
	// Bucket key of the object in S3 bucket
	Key string
	// Region is AWS region
	Region string
	// Encrypt indicates whether state is encrypted
	Encrypt bool
}

type s3StateURL string

// String implements State
func (s s3StateURL) String() string {
	return string(s)
}
