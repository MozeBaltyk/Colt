package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGiteaTLSAPIAuthenticationGetAndCreate(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, visibility, createPath string
		private                                 bool
	}{
		{"user private", "alice", "private", "/api/v1/user/repos", true},
		{"organization public", "team", "public", "/api/v1/orgs/team/repos", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var authRequests atomic.Int32
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "token gitea-token" {
					t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
					authRequests.Add(1)
					fmt.Fprint(w, `{"login":"alice"}`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/"+tc.namespace+"/demo":
					fmt.Fprintf(w, `{"clone_url":%q,"ssh_url":%q}`, server.URL+"/"+tc.namespace+"/demo.git", "git@"+strings.TrimPrefix(server.URL, "https://")+":"+tc.namespace+"/demo.git")
				case r.Method == http.MethodPost && r.URL.Path == tc.createPath:
					var body struct {
						Name    string `json:"name"`
						Private bool   `json:"private"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name != "demo" || body.Private != tc.private {
						t.Errorf("create body = %+v, %v", body, err)
					}
					fmt.Fprintf(w, `{"clone_url":%q}`, server.URL+"/"+tc.namespace+"/demo.git")
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			settings := testSettings("gitea", server.URL)
			settings.Namespace, settings.Visibility = tc.namespace, tc.visibility
			client, err := New(settings, "gitea-token", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if account, err := client.Authenticate(context.Background()); err != nil || account != "alice" {
				t.Fatalf("Authenticate() = %q, %v", account, err)
			}
			if repo, err := client.Get(context.Background(), "demo"); err != nil || repo.CloneURL != server.URL+"/"+tc.namespace+"/demo.git" {
				t.Fatalf("Get() = %#v, %v", repo, err)
			}
			if repo, err := client.Create(context.Background(), "demo"); err != nil || repo.CloneURL != server.URL+"/"+tc.namespace+"/demo.git" {
				t.Fatalf("Create() = %#v, %v", repo, err)
			}
			if got := authRequests.Load(); got != 1 {
				t.Fatalf("authentication requests = %d, want 1", got)
			}
		})
	}
}

func TestGiteaMapsNotFoundAndConflict(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/user":
			fmt.Fprint(w, `{"login":"team"}`)
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusConflict)
		}
	}))
	defer server.Close()
	settings := testSettings("gitea", server.URL)
	settings.Namespace = "team"
	client, err := New(settings, "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), "demo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := client.Create(context.Background(), "demo"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestGiteaRejectsUnauthoritativeOrUncleanCloneURL(t *testing.T) {
	for _, cloneURL := range []string{"https://evil.example/team/demo.git", "https://code.example/team/demo.git?token=secret"} {
		t.Run(cloneURL, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"clone_url":%q}`, cloneURL)
			}))
			defer server.Close()
			client, err := New(testSettings("gitea", server.URL), "token", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Get(context.Background(), "demo"); err == nil {
				t.Fatalf("accepted clone URL %q", cloneURL)
			}
		})
	}
}
