package rules

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"math/rand"
	"os"
	"regexp"
	"time"

	grpcCodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	"github.com/b0ch3nski/go-starlink/model/internal/metrics"
	yaml "gopkg.in/yaml.v3"
)

// Rule defines a single routing / mutation instruction.
// Minimal initial DSL; can be extended.
type Rule struct {
	Name    string        `yaml:"name"`
	Match   MatchCriteria `yaml:"match"`
	Actions []Action      `yaml:"actions"`
	Stop    bool          `yaml:"stop"` // stop processing further rules when applied
}

type MatchCriteria struct {
	RequestTypes []string `yaml:"request_types"` // e.g. dish_get_status, wifi_get_status (lowercase)
	TargetRegex  string   `yaml:"target_regex"`
	Probability  float64  `yaml:"probability"` // 0..1
}

type Action struct {
	Type string `yaml:"type"` // delay | status | log | jitter | field_override | error | drop
	// delay / jitter
	MS       int `yaml:"ms"`
	JitterMs int `yaml:"jitter_ms"` // +/- random additional delay
	// status
	Code    int    `yaml:"code"`
	Message string `yaml:"message"`
	// field_override (shallow limited support for a few numeric fields by key)
	Field string      `yaml:"field"`
	Value interface{} `yaml:"value"`
	// error injection
	ErrorCode string `yaml:"error_code"` // e.g. unavailable, internal, canceled
}

// PostResult reports side effects after ApplyPost.
type PostResult struct {
	Err  error
	Drop bool
}

// Engine holds compiled rules.
type Engine struct {
	rules []compiledRule
}

type compiledRule struct {
	raw      Rule
	targetRe *regexp.Regexp
}

// Load loads rules from YAML file (list of Rule documents).
func Load(path string) (*Engine, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []Rule
	if err := yaml.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	cr := make([]compiledRule, 0, len(list))
	for _, r := range list {
		if r.Match.Probability == 0 {
			r.Match.Probability = 1
		}
		var re *regexp.Regexp
		if r.Match.TargetRegex != "" {
			re, err = regexp.Compile(r.Match.TargetRegex)
			if err != nil {
				return nil, err
			}
		}
		cr = append(cr, compiledRule{raw: r, targetRe: re})
	}
	return &Engine{rules: cr}, nil
}

// Exists returns true if file exists.
func Exists(path string) bool { _, err := os.Stat(path); return err == nil }

// ApplyPre can inject delays before request handling.
func (e *Engine) ApplyPre(ctx context.Context, req *dev.Request) error {
	if e == nil {
		return nil
	}
	for _, cr := range e.rules {
		if !cr.matches(req) {
			continue
		}
		metrics.RuleHit(cr.raw.Name)
		for _, a := range cr.raw.Actions {
			switch a.Type {
			case "delay", "jitter":
				total := a.MS
				if a.Type == "jitter" && a.JitterMs > 0 {
					delta := rand.Intn(a.JitterMs*2+1) - a.JitterMs // [-JitterMs,+JitterMs]
					total += delta
					if total < 0 {
						total = 0
					}
				}
				if total > 0 {
					select {
					case <-time.After(time.Duration(total) * time.Millisecond):
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
		}
		if cr.raw.Stop {
			break
		}
	}
	return nil
}

// ApplyPost mutates the response (status override, logging, etc.).
func (e *Engine) ApplyPost(resp *dev.Response, req *dev.Request) PostResult {
	var out PostResult
	if e == nil || resp == nil {
		return out
	}
	for _, cr := range e.rules {
		if !cr.matches(req) {
			continue
		}
		for _, a := range cr.raw.Actions {
			switch a.Type {
			case "status":
				if resp.Status != nil {
					resp.Status.Code = int32(a.Code)
					if a.Message != "" {
						resp.Status.Message = a.Message
					}
				}
			case "log":
				if a.Message != "" {
					log.Printf("rule[%s]: %s", cr.raw.Name, a.Message)
				}
			case "field_override":
				applyFieldOverride(resp, a.Field, a.Value)
			case "error":
				out.Err = grpcstatus.Error(stringToCode(a.ErrorCode), firstNonEmpty(a.Message, "injected error"))
			case "drop":
				out.Drop = true
			}
		}
		if cr.raw.Stop {
			break
		}
	}
	return out
}

func stringToCode(s string) grpcCodes.Code {
	switch s {
	case "canceled":
		return grpcCodes.Canceled
	case "unknown":
		return grpcCodes.Unknown
	case "invalid_argument":
		return grpcCodes.InvalidArgument
	case "deadline_exceeded":
		return grpcCodes.DeadlineExceeded
	case "not_found":
		return grpcCodes.NotFound
	case "already_exists":
		return grpcCodes.AlreadyExists
	case "permission_denied":
		return grpcCodes.PermissionDenied
	case "resource_exhausted":
		return grpcCodes.ResourceExhausted
	case "failed_precondition":
		return grpcCodes.FailedPrecondition
	case "aborted":
		return grpcCodes.Aborted
	case "out_of_range":
		return grpcCodes.OutOfRange
	case "unimplemented":
		return grpcCodes.Unimplemented
	case "internal":
		return grpcCodes.Internal
	case "unavailable":
		return grpcCodes.Unavailable
	case "data_loss":
		return grpcCodes.DataLoss
	case "unauthenticated":
		return grpcCodes.Unauthenticated
	default:
		return grpcCodes.Unknown
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (cr *compiledRule) matches(req *dev.Request) bool {
	r := cr.raw.Match
	if r.Probability < 1 {
		if rand.Float64() > r.Probability {
			return false
		}
	}
	if len(r.RequestTypes) > 0 {
		key := requestKey(req)
		found := false
		for _, k := range r.RequestTypes {
			if k == key {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if cr.targetRe != nil {
		if !cr.targetRe.MatchString(req.GetTargetId()) {
			return false
		}
	}
	return true
}

// requestKey converts the active oneof request into a stable lowercase key.
func requestKey(req *dev.Request) string {
	if req == nil || req.GetRequest() == nil {
		return ""
	}
	switch req.GetRequest().(type) {
	case *dev.Request_GetStatus:
		return "get_status"
	case *dev.Request_DishGetContext:
		return "dish_get_context"
	case *dev.Request_WifiGetClients:
		return "wifi_get_clients"
	case *dev.Request_GetPing:
		return "get_ping"
	case *dev.Request_PingHost:
		return "ping_host"
	case *dev.Request_SpeedTest:
		return "speed_test"
	case *dev.Request_DishGetConfig:
		return "dish_get_config"
	case *dev.Request_WifiGetConfig:
		return "wifi_get_config"
	default:
		return "other"
	}
}

// WriteTemplate writes a starter rules file if not present.
func WriteTemplate(path string, perm fs.FileMode) error {
	if Exists(path) {
		return nil
	}
	sample := `# Example rules for naquadah\n` +
		`# Delay and inject status error for some status calls\n` +
		`- name: inject_latency_and_error\n` +
		`  match:\n` +
		`    request_types: ["dish_get_status", "wifi_get_status"]\n` +
		`    probability: 0.3\n` +
		`  actions:\n` +
		`    - type: delay\n` +
		`      ms: 500\n` +
		`    - type: status\n` +
		`      code: 13\n` +
		`      message: "INJECTED_UNAVAILABLE"\n` +
		`    - type: log\n` +
		`      message: "Injected 500ms delay + error"\n` +
		`  stop: true\n`
	return os.WriteFile(path, []byte(sample), perm)
}

// Validate ensures rule list isn't empty.
func (e *Engine) Validate() error {
	if e == nil || len(e.rules) == 0 {
		return errors.New("no rules loaded")
	}
	return nil
}

// applyFieldOverride is a narrow helper to override a few known numeric fields.
// field naming: dish.downlink_bps, wifi.ping_latency_ms
func applyFieldOverride(resp *dev.Response, field string, value interface{}) {
	if resp == nil || field == "" || value == nil {
		return
	}
	switch v := resp.Response.(type) {
	case *dev.Response_DishGetStatus:
		if v.DishGetStatus == nil {
			return
		}
		switch field {
		case "dish.downlink_bps":
			if f, ok := numToFloat(value); ok {
				v.DishGetStatus.DownlinkThroughputBps = float32(f)
			}
		case "dish.uplink_bps":
			if f, ok := numToFloat(value); ok {
				v.DishGetStatus.UplinkThroughputBps = float32(f)
			}
		case "dish.pop_ping_latency_ms":
			if f, ok := numToFloat(value); ok {
				v.DishGetStatus.PopPingLatencyMs = float32(f)
			}
		}
	case *dev.Response_WifiGetStatus:
		if v.WifiGetStatus == nil {
			return
		}
		switch field {
		case "wifi.ping_latency_ms":
			if f, ok := numToFloat(value); ok {
				v.WifiGetStatus.PingLatencyMs = float32(f)
			}
		case "wifi.downlink_bps":
			if f, ok := numToFloat(value); ok {
				v.WifiGetStatus.DishPingLatencyMs = float32(f)
			}
		case "wifi.ping_drop_rate":
			if f, ok := numToFloat(value); ok {
				v.WifiGetStatus.PingDropRate = float32(f)
			}
		}
	}
}

func numToFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}
