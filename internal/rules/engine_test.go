package rules

import (
	"context"
	"testing"
	"time"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
	statuspb "github.com/b0ch3nski/go-starlink/model/api-protoc/status"
)

func TestEngineDelayAndStatus(t *testing.T) {
	eng := &Engine{rules: []compiledRule{}}
	eng.rules = append(eng.rules, compiledRule{raw: Rule{
		Name:    "delay_and_status",
		Match:   MatchCriteria{RequestTypes: []string{"get_status"}, Probability: 1},
		Actions: []Action{{Type: "delay", MS: 50}, {Type: "status", Code: 42, Message: "RULE"}},
		Stop:    true,
	}})
	req := &dev.Request{Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}}
	ctx := context.Background()
	start := time.Now()
	if err := eng.ApplyPre(ctx, req); err != nil {
		t.Fatalf("pre: %v", err)
	}
	dur := time.Since(start)
	if dur < 45*time.Millisecond {
		t.Fatalf("expected delay, got %v", dur)
	}
	resp := &dev.Response{Status: &statuspb.Status{Code: 0, Message: "OK"}}
	eng.ApplyPost(resp, req)
	if resp.Status.Code != 42 || resp.Status.Message != "RULE" {
		t.Fatalf("status override failed: %+v", resp.Status)
	}
}

func TestProbability(t *testing.T) {
	eng := &Engine{rules: []compiledRule{}}
	eng.rules = append(eng.rules, compiledRule{raw: Rule{
		Name:    "sometimes",
		Match:   MatchCriteria{RequestTypes: []string{"get_status"}, Probability: 0.0},
		Actions: []Action{{Type: "status", Code: 9}},
	}})
	req := &dev.Request{Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}}
	resp := &dev.Response{Status: &statuspb.Status{Code: 0}}
	eng.ApplyPost(resp, req)
	if resp.Status.Code != 0 {
		t.Fatalf("rule should not have fired with prob 0")
	}
}

func TestErrorAndDrop(t *testing.T) {
	eng := &Engine{rules: []compiledRule{}}
	eng.rules = append(eng.rules, compiledRule{raw: Rule{
		Name:    "inject_error",
		Match:   MatchCriteria{RequestTypes: []string{"get_status"}, Probability: 1},
		Actions: []Action{{Type: "error", ErrorCode: "unavailable", Message: "boom"}},
		Stop:    true,
	}})
	req := &dev.Request{Request: &dev.Request_GetStatus{GetStatus: &dev.GetStatusRequest{}}}
	resp := &dev.Response{Status: &statuspb.Status{Code: 0}}
	pr := eng.ApplyPost(resp, req)
	if pr.Err == nil {
		t.Fatalf("expected injected error")
	}

	// drop after error rule won't run due to stop
	eng.rules = []compiledRule{compiledRule{raw: Rule{
		Name:    "drop",
		Match:   MatchCriteria{RequestTypes: []string{"get_status"}, Probability: 1},
		Actions: []Action{{Type: "drop"}},
	}}}
	pr = eng.ApplyPost(resp, req)
	if !pr.Drop {
		t.Fatalf("expected drop")
	}
}
