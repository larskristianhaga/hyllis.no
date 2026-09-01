// Package auth wires this app to Supabase Auth (GoTrue): a thin REST client
// for signup/login/refresh/logout, ES256 JWKS-based access-token
// verification, and the session-cookie/middleware glue between them. Go
// never mints or stores credentials itself — Supabase owns the auth flow,
// per CLAUDE.md.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Tokens is the pair of Supabase-issued tokens that make up a session: the
// short-lived access token (verified per-request against Supabase's JWKS)
// and the longer-lived refresh token (used to mint a new access token
// without the user re-entering credentials).
type Tokens struct {
	AccessToken  string
	RefreshToken string
}

// User is the subset of Supabase's user object this app reads.
type User struct {
	ID           string         `json:"id"`
	Email        string         `json:"email"`
	UserMetadata map[string]any `json:"user_metadata"`
}

// DisplayName returns the "name" field from Supabase's user_metadata if
// present (set at signup, see Client.SignUp), otherwise falls back to the
// email so callers always have something to show/store.
func (u User) DisplayName() string {
	if name, ok := u.UserMetadata["name"].(string); ok && name != "" {
		return name
	}
	return u.Email
}

// Session is a signup/login response: the issued tokens plus the user they
// belong to. Tokens is zero-valued when SignUp succeeds but Supabase
// requires email confirmation before a session exists — see SignUp.
type Session struct {
	Tokens
	User User
}

// gotrueResponse mirrors the fields Supabase's GoTrue token/signup
// endpoints return, flattened across both shapes (a bare user object when
// no session is issued yet, or user+session together).
type gotrueResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         *User  `json:"user"`
	// Signup's response nests the user fields at top level instead of
	// under "user" when no session is issued (email confirmation
	// pending) — these mirror User's fields for that case.
	ID           string         `json:"id"`
	Email        string         `json:"email"`
	UserMetadata map[string]any `json:"user_metadata"`
}

func (r gotrueResponse) session() Session {
	if r.User != nil {
		return Session{Tokens: Tokens{AccessToken: r.AccessToken, RefreshToken: r.RefreshToken}, User: *r.User}
	}
	return Session{
		Tokens: Tokens{AccessToken: r.AccessToken, RefreshToken: r.RefreshToken},
		User:   User{ID: r.ID, Email: r.Email, UserMetadata: r.UserMetadata},
	}
}

// gotrueError mirrors Supabase's error response shapes across GoTrue
// versions.
type gotrueError struct {
	ErrorDescription string `json:"error_description"`
	Error            string `json:"error"`
	Msg              string `json:"msg"`
}

func (e gotrueError) message() string {
	switch {
	case e.Msg != "":
		return e.Msg
	case e.ErrorDescription != "":
		return e.ErrorDescription
	case e.Error != "":
		return e.Error
	default:
		return ""
	}
}

// Client is a thin REST client for the Supabase Auth (GoTrue) endpoints
// this app needs. It holds no credentials beyond the project's own
// publishable/anon key, which Supabase's API expects on every request via
// the "apikey" header.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient builds a Client for supabaseURL (e.g. "https://xyz.supabase.co")
// using apiKey (the project's publishable/anon key) on every request.
func NewClient(supabaseURL, apiKey string) *Client {
	return &Client{baseURL: supabaseURL, apiKey: apiKey, http: http.DefaultClient}
}

// SignUp registers a new user with email/password, and name stored in
// user_metadata for User.DisplayName to read later. If the Supabase project
// requires email confirmation, the returned Session has a zero Tokens (no
// session yet — the caller should tell the user to check their email
// instead of treating this as a failure).
func (c *Client) SignUp(ctx context.Context, email, password, name string) (Session, error) {
	body := map[string]any{
		"email":    email,
		"password": password,
		"data":     map[string]any{"name": name},
	}
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/signup", body)
	if err != nil {
		return Session{}, err
	}
	return resp.session(), nil
}

// SignInWithPassword exchanges email/password for a Session via Supabase's
// password grant.
func (c *Client) SignInWithPassword(ctx context.Context, email, password string) (Session, error) {
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/token?grant_type=password", map[string]any{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return Session{}, err
	}
	return resp.session(), nil
}

// Refresh exchanges a refresh token for a new Session, without the user
// re-entering credentials.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/token?grant_type=refresh_token", map[string]any{
		"refresh_token": refreshToken,
	})
	if err != nil {
		return Session{}, err
	}
	return resp.session(), nil
}

// SignOut revokes accessToken's session server-side (best-effort — callers
// should clear their local session cookies regardless of the outcome here).
func (c *Client) SignOut(ctx context.Context, accessToken string) error {
	req, err := c.newRequest(ctx, http.MethodPost, "/auth/v1/logout", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("auth: sign out: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("auth: sign out: %s", statusErr(resp))
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("auth: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("auth: build request: %w", err)
	}
	req.Header.Set("apikey", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) (gotrueResponse, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return gotrueResponse{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return gotrueResponse{}, fmt.Errorf("auth: request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return gotrueResponse{}, fmt.Errorf("auth: %s: %s", path, statusErr(resp))
	}

	var out gotrueResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return gotrueResponse{}, fmt.Errorf("auth: decode response from %s: %w", path, err)
	}
	return out, nil
}

// statusErr reads resp's body (already known to be an error response) and
// returns the best human-readable message it can extract from it.
func statusErr(resp *http.Response) string {
	data, _ := io.ReadAll(resp.Body)
	var gerr gotrueError
	if err := json.Unmarshal(data, &gerr); err == nil {
		if msg := gerr.message(); msg != "" {
			return msg
		}
	}
	if len(data) > 0 {
		return string(data)
	}
	return resp.Status
}
