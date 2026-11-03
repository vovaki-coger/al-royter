package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const neonAPI = "https://console.neon.tech/api/v2"

// isNeonAPIKey reports whether the value looks like a Neon API key rather than a DSN.
func isNeonAPIKey(v string) bool {
	return strings.HasPrefix(v, "napi_") ||
		strings.HasPrefix(v, "neon_api_key_") ||
		strings.HasPrefix(v, "neon_org_key_") ||
		strings.HasPrefix(v, "neon_project_key_")
}

// resolveNeonURI asks the Neon API for the connection string of the first project.
func resolveNeonURI(apiKey string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}

	get := func(path string, out interface{}) error {
		req, err := http.NewRequest("GET", neonAPI+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			return fmt.Errorf("neon api %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}

	// Personal Neon API keys need an explicit org_id since 2026.
	var orgs struct {
		Organizations []struct {
			ID string `json:"id"`
		} `json:"organizations"`
	}
	orgPath := "/projects"
	if err := get("/users/me/organizations", &orgs); err == nil && len(orgs.Organizations) > 0 {
		orgPath = "/projects?org_id=" + url.QueryEscape(orgs.Organizations[0].ID)
	}

	var projects struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := get(orgPath, &projects); err != nil {
		return "", err
	}
	if len(projects.Projects) == 0 {
		return "", fmt.Errorf("no Neon projects found for this API key")
	}
	projectID := projects.Projects[0].ID

	var branches struct {
		Branches []struct {
			ID      string `json:"id"`
			Default bool   `json:"default"`
			Primary bool   `json:"primary"`
		} `json:"branches"`
	}
	if err := get("/projects/"+projectID+"/branches", &branches); err != nil {
		return "", err
	}
	branchID := ""
	for _, b := range branches.Branches {
		if b.Default || b.Primary {
			branchID = b.ID
			break
		}
	}
	if branchID == "" && len(branches.Branches) > 0 {
		branchID = branches.Branches[0].ID
	}
	if branchID == "" {
		return "", fmt.Errorf("no branches in project %s", projectID)
	}

	var dbs struct {
		Databases []struct {
			Name      string `json:"name"`
			OwnerName string `json:"owner_name"`
		} `json:"databases"`
	}
	if err := get("/projects/"+projectID+"/branches/"+branchID+"/databases", &dbs); err != nil {
		return "", err
	}
	if len(dbs.Databases) == 0 {
		return "", fmt.Errorf("no databases in project %s", projectID)
	}
	dbName := dbs.Databases[0].Name
	roleName := dbs.Databases[0].OwnerName

	q := url.Values{}
	q.Set("branch_id", branchID)
	q.Set("database_name", dbName)
	q.Set("role_name", roleName)
	q.Set("pooled", "true")

	var uri struct {
		URI string `json:"uri"`
	}
	if err := get("/projects/"+projectID+"/connection_uri?"+q.Encode(), &uri); err != nil {
		return "", err
	}
	if uri.URI == "" {
		return "", fmt.Errorf("neon returned empty connection uri")
	}
	// lib/pq does not understand libpq client options like channel_binding.
	uri.URI = strings.ReplaceAll(uri.URI, "channel_binding=require&", "")
	uri.URI = strings.ReplaceAll(uri.URI, "&channel_binding=require", "")
	if !strings.Contains(uri.URI, "sslmode=") {
		sep := "?"
		if strings.Contains(uri.URI, "?") {
			sep = "&"
		}
		uri.URI += sep + "sslmode=require"
	}
	return uri.URI, nil
}