package provider

import (
	"testing"

	"github.com/MozeBaltyk/Colt/internal/config"
)

func TestRepositoryHealthMetadataValidation(t *testing.T) {
	private := true
	c := &client{settings: config.Provider{Type: "gitea", Host: "code.example"}}
	result := repositoryResponse{Name: "demo", FullName: "team/demo", CloneURL: "https://code.example/team/demo.git", DefaultBranch: "main", Private: &private}
	repository, err := c.repository(result)
	if err != nil || repository.DefaultBranch != "main" || repository.Visibility != "private" {
		t.Fatalf("repository=%+v error=%v", repository, err)
	}
	result.DefaultBranch = "bad..branch"
	if _, err := c.repository(result); err == nil {
		t.Fatal("invalid provider default branch accepted")
	}
	result.DefaultBranch, result.Visibility = "main", "secret"
	if _, err := c.repository(result); err == nil {
		t.Fatal("invalid provider visibility accepted")
	}
}
