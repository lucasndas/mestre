package github

import (
	"context"
	"net/http"
)

// RepoData is what GitHub says about a repository.
type RepoData struct {
	// FullName is the name as GitHub spells it ("lucas/Meu_App").
	FullName string
	// CloneURL is the https URL, without credentials.
	CloneURL      string
	DefaultBranch string
}

// Repository reads a repository (ErrNotFound for one that does not exist or
// where the App is not installed).
func (c *REST) Repository(ctx context.Context, repo string) (RepoData, error) {
	var v struct {
		FullName      string `json:"full_name"`
		CloneURL      string `json:"clone_url"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.send(ctx, http.MethodGet, repo, "", nil, http.StatusOK, &v, "reading a repository"); err != nil {
		return RepoData{}, err
	}
	return RepoData{FullName: v.FullName, CloneURL: v.CloneURL, DefaultBranch: v.DefaultBranch}, nil
}
