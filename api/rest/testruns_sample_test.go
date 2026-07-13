package rest

import (
	"context"
	"strings"
	"testing"

	"runtime.link/api"
)

// sampleAPI is a minimal rest-tagged API whose call is captured in the trace.
type sampleAPI struct {
	api.Specification

	Echo func(context.Context, string) (string, error) `rest:"POST /echo (message) message"`
}

// sampleSuite is a test suite that exercises sampleAPI so the recorded trace
// should carry a sampled downstream request/response.
type sampleSuite struct {
	api.TestingFramework

	Sample sampleAPI
}

// TestEcho calls the rest-tagged Echo endpoint.
func (s *sampleSuite) TestEcho(ctx context.Context) error {
	_, err := s.Sample.Echo(ctx, "hello")
	return err
}

func newSampleSuite(ctx context.Context) (api.Examples, error) {
	return &sampleSuite{
		Sample: sampleAPI{
			Echo: func(ctx context.Context, message string) (string, error) { return message, nil },
		},
	}, nil
}

// TestTraceSamplesDownstreamHTTP verifies that the sampler registered by this
// package's init attaches the downstream HTTP url/request/response to the
// recorded trace events for calls that carry a rest tag.
func TestTraceSamplesDownstreamHTTP(t *testing.T) {
	var doc api.Documentation = newSampleSuite
	exec, ok := doc.Test(t.Context(), "TestEcho")
	if !ok {
		t.Fatal("TestEcho did not run")
	}
	var found bool
	for _, event := range exec.Trace {
		if event.Call != "Echo" {
			continue
		}
		found = true
		if event.URL == "" {
			t.Error("Echo event has no sampled URL")
		}
		if !strings.HasPrefix(event.URL, "POST ") || !strings.Contains(event.URL, "/echo") {
			t.Errorf("Echo URL = %q, want POST .../echo", event.URL)
		}
		if len(event.Resp) == 0 {
			t.Error("Echo event has no sampled response")
		}
		if !strings.Contains(string(event.Resp), "hello") {
			t.Errorf("Echo response = %q, want it to contain the echoed message", event.Resp)
		}
	}
	if !found {
		t.Fatalf("no Echo call captured in trace: %+v", exec.Trace)
	}
}
