package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGitHubDeviceFlowPollsAndReturnsTokenWithoutDisplayingIt(t *testing.T) {
	polls := 0
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.Header.Get("Accept") != "application/json" {
			t.Fatalf("request=%s %s headers=%v", req.Method, req.URL, req.Header)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil || form.Get("client_id") != "Ov23liERaF30k06vMN8L" {
			t.Fatalf("form=%v error=%v", form, err)
		}
		response := `{"device_code":"device-code","user_code":"USER-CODE","verification_uri":"https://github.com/login/device","expires_in":600,"interval":1}`
		if req.URL.Path == "/login/oauth/access_token" {
			polls++
			switch polls {
			case 1:
				response = `{"error":"authorization_pending"}`
			case 2:
				response = `{"error":"slow_down"}`
			default:
				response = `{"access_token":"device-secret","token_type":"bearer"}`
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	var waits []time.Duration
	githubDeviceWait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	defer func() { githubDeviceWait = nil }()
	var output bytes.Buffer
	token, err := authorizeGitHubDevice(context.Background(), hc, githubDeviceBaseURL, &output, githubDeviceWait)
	if err != nil || token != "device-secret" || !reflect.DeepEqual(waits, []time.Duration{time.Second, time.Second, 6 * time.Second}) {
		t.Fatalf("token=%q error=%v waits=%v", token, err, waits)
	}
	if !strings.Contains(output.String(), "USER-CODE") || strings.Contains(output.String(), token) {
		t.Fatalf("unsafe output %q", output.String())
	}
}
