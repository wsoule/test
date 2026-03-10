package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"

	"inreview/internal/rdb"
)

// GenerateSessionID returns a 32-byte hex random token suitable for use as a
// session cookie value. crypto/rand ensures it is not guessable.
func GenerateSessionID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// GenerateOAuthState stores a one-time random state in Redis (10-min TTL)
// and returns it for embedding in the OAuth redirect URL.
func GenerateOAuthState(ctx context.Context, cache *rdb.Client) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := fmt.Sprintf("%x", b)
	cache.Set(ctx, "oauth_state:"+state, []byte("1"), 10*time.Minute)
	return state
}

// ValidateOAuthState checks and atomically deletes the state from Redis.
// Returns true only if the state existed (single-use CSRF token).
func ValidateOAuthState(ctx context.Context, cache *rdb.Client, state string) bool {
	if state == "" {
		return false
	}
	_, ok := cache.Get(ctx, "oauth_state:"+state)
	if ok {
		cache.Del(ctx, "oauth_state:"+state)
	}
	return ok
}

// ParsePrivateKey parses a PEM-encoded RSA private key (PKCS#1 or PKCS#8).
func ParsePrivateKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, fmt.Errorf("auth: failed to decode PEM block from private key")
	}
	// Try PKCS#1 first (most common for GitHub Apps)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err == nil {
		return key, nil
	}
	// Fall back to PKCS#8
	parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err2 != nil {
		return nil, fmt.Errorf("auth: failed to parse private key (pkcs1: %v, pkcs8: %v)", err, err2)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("auth: private key is not RSA")
	}
	return rsaKey, nil
}

// GenerateAppJWT creates a RS256-signed JWT for authenticating as a GitHub App.
// GitHub App JWTs are valid for up to 10 minutes and are used to request
// installation access tokens via /app/installations/{id}/access_tokens.
func GenerateAppJWT(appID int64, privateKey *rsa.PrivateKey) (string, error) {
	now := time.Now()
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claimsJSON, _ := json.Marshal(map[string]interface{}{
		"iat": now.Add(-60 * time.Second).Unix(), // 60s skew allowance
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", appID),
	})

	h64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	c64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	sigInput := h64 + "." + c64

	digest := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("auth: signing JWT: %w", err)
	}
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// installationTokenResp is the GitHub API response for a fresh installation token.
type installationTokenResp struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GetInstallationToken exchanges an App JWT for a short-lived installation
// access token via the GitHub REST API. The token expires in ~1 hour.
func GetInstallationToken(appJWT, installationID string) (string, time.Time, error) {
	url := fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", installationID)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("auth: get installation token: status %d: %s", resp.StatusCode, body)
	}

	var tok installationTokenResp
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", time.Time{}, err
	}
	return tok.Token, tok.ExpiresAt, nil
}

// ExchangeOAuthCode exchanges a GitHub OAuth code for a user access token.
// We use this only to fetch the user's login (immediately discarded afterwards).
func ExchangeOAuthCode(clientID, clientSecret, code, redirectURI string) (string, error) {
	req, err := http.NewRequest("POST", "https://github.com/login/oauth/access_token", nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("client_id", clientID)
	q.Set("client_secret", clientSecret)
	q.Set("code", code)
	if redirectURI != "" {
		q.Set("redirect_uri", redirectURI)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if errMsg, ok := result["error"]; ok {
		return "", fmt.Errorf("oauth error: %v: %v", errMsg, result["error_description"])
	}
	token, _ := result["access_token"].(string)
	if token == "" {
		return "", fmt.Errorf("auth: no access_token in OAuth response")
	}
	return token, nil
}

// FetchGitHubLogin returns the GitHub login of the user identified by the given
// OAuth access token. The token is used only for this one call.
func FetchGitHubLogin(token string) (string, string, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var user struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", "", err
	}
	if user.Login == "" {
		return "", "", fmt.Errorf("auth: empty login in GitHub user response")
	}
	return user.Login, user.AvatarURL, nil
}

// ListInstallationRepos returns the full_names of repos accessible via the
// installation token (e.g. "owner/repo").
func ListInstallationRepos(installationToken string) ([]string, error) {
	url := "https://api.github.com/installation/repositories?per_page=100"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+installationToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Repositories []struct {
			FullName string `json:"full_name"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	names := make([]string, len(result.Repositories))
	for i, repo := range result.Repositories {
		names[i] = repo.FullName
	}
	return names, nil
}
