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

	"github.com/MozeBaltyk/Colt/internal/config"
)

func testSettings(kind, base string) config.Provider {
	return config.Provider{Type: kind, Host: strings.TrimPrefix(base, "https://"), BaseURL: base, Namespace: "team/sub", Visibility: "private", GitName: "Test", GitEmail: "test@example.com", Auth: config.Auth{Source: "env"}}
}

func TestCORE_PROVIDER_001GitHubHTTPSAPIUserAndOrganization(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, account, createPath string
	}{
		{"user", "octocat", "octocat", "/user/repos"},
		{"organization", "acme", "octocat", "/orgs/acme/repos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.EscapedPath())
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("GitHub bearer token missing")
				}
				switch r.URL.Path {
				case "/user":
					fmt.Fprintf(w, `{"login":%q}`, tc.account)
				case "/repos/" + tc.namespace + "/demo":
					w.WriteHeader(http.StatusNotFound)
				case tc.createPath:
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["name"] != "demo" || body["private"] != true {
						t.Errorf("create body = %#v", body)
					}
					fmt.Fprintf(w, `{"clone_url":%q,"ssh_url":%q}`, server.URL+"/"+tc.namespace+"/demo.git", "git@github.com:"+tc.namespace+"/demo.git")
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			settings := testSettings("github", server.URL)
			settings.Namespace = tc.namespace
			client, _ := New(settings, "test-token", server.Client())
			if _, err := client.Get(context.Background(), "demo"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get() error = %v", err)
			}
			repo, err := client.Create(context.Background(), "demo")
			if err != nil || repo.CloneURL != server.URL+"/"+tc.namespace+"/demo.git" || repo.SSHURL != "git@github.com:"+tc.namespace+"/demo.git" {
				t.Fatalf("Create() = %#v, %v; paths %v", repo, err, paths)
			}
		})
	}
}

func TestCORE_PROVIDER_003GitLabConfiguredBaseAndSafeNamespace(t *testing.T) {
	var sawNamespace, sawCreate bool
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "lab-token" {
			t.Error("GitLab token missing")
		}
		switch {
		case r.URL.Path == "/api/v4/user":
			fmt.Fprint(w, `{"username":"alice"}`)
		case strings.HasPrefix(r.URL.Path, "/api/v4/projects/"):
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/v4/namespaces/team/sub":
			sawNamespace = true
			fmt.Fprint(w, `{"id":42,"full_path":"team/sub"}`)
		case r.URL.Path == "/api/v4/projects" && r.Method == http.MethodPost:
			sawCreate = true
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["namespace_id"] != float64(42) || body["visibility"] != "private" {
				t.Errorf("create body = %#v", body)
			}
			fmt.Fprintf(w, `{"http_url_to_repo":%q,"ssh_url_to_repo":"git@gitlab.com:team/sub/demo.git"}`, server.URL+"/team/sub/demo.git")
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, _ := New(testSettings("gitlab", server.URL), "lab-token", server.Client())
	if account, err := client.Authenticate(context.Background()); err != nil || account != "alice" {
		t.Fatalf("Authenticate() = %q, %v", account, err)
	}
	if _, err := client.Get(context.Background(), "demo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v", err)
	}
	repo, err := client.Create(context.Background(), "demo")
	if err != nil || repo.CloneURL != server.URL+"/team/sub/demo.git" || repo.SSHURL != "git@gitlab.com:team/sub/demo.git" || !sawNamespace || !sawCreate {
		t.Fatalf("Create() = %#v, %v; namespace=%v create=%v", repo, err, sawNamespace, sawCreate)
	}
}

func TestCORE_PROVIDER_004CrossHostRedirectNeverReceivesCredentials(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			var targetRequests atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetRequests.Add(1) }))
			defer target.Close()
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
			}))
			defer source.Close()
			settings := testSettings(kind, source.URL)
			client, _ := New(settings, "redirect-secret", source.Client())
			_, err := client.Authenticate(context.Background())
			if err == nil || !strings.Contains(err.Error(), "refusing provider redirect") {
				t.Fatalf("Authenticate() error = %v", err)
			}
			if targetRequests.Load() != 0 {
				t.Fatal("redirect target received a request")
			}
		})
	}
}

func TestCORE_CONFLICT_001ProviderCreationConflictIsAuthoritative(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "github" && r.URL.Path == "/user" {
					fmt.Fprint(w, `{"login":"team"}`)
					return
				}
				if kind == "gitlab" && strings.HasPrefix(r.URL.Path, "/api/v4/namespaces/") {
					fmt.Fprint(w, `{"id":7,"full_path":"team/sub"}`)
					return
				}
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"message":"already exists"}`)
			}))
			defer server.Close()
			settings := testSettings(kind, server.URL)
			if kind == "github" {
				settings.Namespace = "team"
			}
			client, _ := New(settings, "token", server.Client())
			_, err := client.Create(context.Background(), "demo")
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("Create() error = %v", err)
			}
		})
	}
}

func TestCORE_CREDENTIAL_002ErrorsAreBoundedAndRedacted(t *testing.T) {
	const token = "secret-token-value"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, token+strings.Repeat("x", 20<<10))
	}))
	defer server.Close()
	client, _ := New(testSettings("github", server.URL), token, server.Client())
	_, err := client.Authenticate(context.Background())
	if err == nil || strings.Contains(err.Error(), token) || len(err.Error()) > 8500 {
		t.Fatalf("unsafe error length=%d: %v", len(err.Error()), err)
	}
}

func TestCORE_CREDENTIAL_002TokenCrossingResponseLimitIsNotDisclosed(t *testing.T) {
	const token = "ZX-secret-token-rest"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, strings.Repeat("a", (8<<10)-2)+token)
	}))
	defer server.Close()
	client, _ := New(testSettings("github", server.URL), token, server.Client())
	_, err := client.Authenticate(context.Background())
	if err == nil || strings.Contains(err.Error(), "ZX") || strings.Contains(err.Error(), strings.Repeat("a", 32)) {
		t.Fatalf("provider response body disclosed: %v", err)
	}
}
