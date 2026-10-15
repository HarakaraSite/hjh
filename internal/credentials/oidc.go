package credentials

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const openIDDiscoveryURL = "https://auth.openai.com/.well-known/openid-configuration"

type openIDConfiguration struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type rsaPublicKey struct {
	key *rsa.PublicKey
}

type idTokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type idTokenClaims struct {
	Issuer   string          `json:"iss"`
	Subject  string          `json:"sub"`
	Audience json.RawMessage `json:"aud"`
	Expires  json.Number     `json:"exp"`
	IssuedAt json.Number     `json:"iat"`
	Nonce    string          `json:"nonce"`
	Email    string          `json:"email"`
}

func (m *Manager) openIDConfiguration(ctx context.Context) (openIDConfiguration, error) {
	m.discoveryMu.Lock()
	defer m.discoveryMu.Unlock()
	if m.discovery.Issuer != "" && time.Since(m.discoveryAt) < 15*time.Minute {
		return m.discovery, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, openIDDiscoveryURL, nil)
	if err != nil {
		return openIDConfiguration{}, errors.New("could not prepare OpenAI discovery request")
	}
	response, err := m.httpClient.Do(request)
	if err != nil {
		return openIDConfiguration{}, fmt.Errorf("load OpenAI sign-in configuration: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return openIDConfiguration{}, fmt.Errorf("load OpenAI sign-in configuration failed with HTTP %d", response.StatusCode)
	}
	var configuration openIDConfiguration
	if err := json.NewDecoder(response.Body).Decode(&configuration); err != nil {
		// Only preserve cancellation identity, not potentially sensitive response bytes.
		if errors.Is(err, context.Canceled) {
			return openIDConfiguration{}, fmt.Errorf("could not read OpenAI sign-in configuration: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return openIDConfiguration{}, fmt.Errorf("could not read OpenAI sign-in configuration: %w", context.DeadlineExceeded)
		}
		return openIDConfiguration{}, errors.New("could not read OpenAI sign-in configuration")
	}
	if configuration.Issuer != "https://auth.openai.com" || configuration.AuthorizationEndpoint == "" || configuration.TokenEndpoint == "" || configuration.JWKSURI == "" {
		return openIDConfiguration{}, errors.New("OpenAI sign-in configuration was incomplete")
	}
	m.discovery = configuration
	m.discoveryAt = time.Now()
	return configuration, nil
}

func (m *Manager) verifyIDToken(ctx context.Context, jwksURI, issuer, token, clientID, nonce string) (idTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return idTokenClaims{}, errors.New("ChatGPT ID token was invalid")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return idTokenClaims{}, errors.New("ChatGPT ID token was invalid")
	}
	var header idTokenHeader
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return idTokenClaims{}, errors.New("ChatGPT ID token used an unsupported signature")
	}
	key, err := m.jwksKey(ctx, jwksURI, header.KeyID)
	if err != nil {
		return idTokenClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return idTokenClaims{}, errors.New("ChatGPT ID token was invalid")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return idTokenClaims{}, errors.New("ChatGPT ID token signature did not validate")
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idTokenClaims{}, errors.New("ChatGPT ID token was invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(claimsBytes)))
	decoder.UseNumber()
	var claims idTokenClaims
	if decoder.Decode(&claims) != nil {
		return idTokenClaims{}, errors.New("ChatGPT ID token claims were invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return idTokenClaims{}, errors.New("ChatGPT ID token claims were invalid")
	}
	if claims.Issuer != issuer || claims.Subject == "" || !audienceIncludes(claims.Audience, clientID) {
		return idTokenClaims{}, errors.New("ChatGPT ID token identity did not match the authorization")
	}
	expires, expErr := claims.Expires.Float64()
	issuedAt, iatErr := claims.IssuedAt.Float64()
	if expErr != nil || iatErr != nil {
		return idTokenClaims{}, errors.New("ChatGPT ID token validity claims were missing")
	}
	// The observed authorization service clock is about 15 seconds ahead
	// of this VM. OIDC allows small clock skew for token validity checks.
	now := float64(time.Now().Unix())
	const clockSkewSeconds = 60
	if expires < now-clockSkewSeconds || issuedAt > now+clockSkewSeconds {
		return idTokenClaims{}, errors.New("ChatGPT ID token was outside its validity period")
	}
	if claims.Nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return idTokenClaims{}, errors.New("ChatGPT ID token nonce did not match")
	}
	return claims, nil
}

func (m *Manager) jwksKey(ctx context.Context, jwksURI, kid string) (*rsa.PublicKey, error) {
	m.jwksMu.Lock()
	defer m.jwksMu.Unlock()
	key, found := m.jwks[kid]
	if found && time.Since(m.jwksAt) < 15*time.Minute {
		return key.key, nil
	}
	keys, err := m.fetchJWKS(ctx, jwksURI)
	if err != nil {
		return nil, err
	}
	m.jwks = keys
	m.jwksAt = time.Now()
	key, found = m.jwks[kid]
	if !found {
		return nil, errors.New("ChatGPT ID token signing key was not published by OpenAI")
	}
	return key.key, nil
}

func (m *Manager) fetchJWKS(ctx context.Context, jwksURI string) (map[string]rsaPublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, errors.New("could not prepare OpenAI signing-key request")
	}
	response, err := m.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("load OpenAI signing keys: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("load OpenAI signing keys failed with HTTP %d", response.StatusCode)
	}
	var document jwksDocument
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		return nil, errors.New("could not read OpenAI signing keys")
	}
	keys := make(map[string]rsaPublicKey, len(document.Keys))
	for _, candidate := range document.Keys {
		if candidate.Kid == "" || candidate.Kty != "RSA" || candidate.Use != "" && candidate.Use != "sig" || candidate.Alg != "" && candidate.Alg != "RS256" {
			continue
		}
		modulus, decodeErr := base64.RawURLEncoding.DecodeString(candidate.N)
		if decodeErr != nil || len(modulus) == 0 {
			continue
		}
		exponentBytes, decodeErr := base64.RawURLEncoding.DecodeString(candidate.E)
		if decodeErr != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			continue
		}
		exponent := 0
		for _, value := range exponentBytes {
			exponent = exponent<<8 | int(value)
		}
		if exponent < 3 || exponent%2 == 0 {
			continue
		}
		keys[candidate.Kid] = rsaPublicKey{key: &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}}
	}
	return keys, nil
}

func audienceIncludes(raw json.RawMessage, clientID string) bool {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == clientID
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, value := range values {
		if value == clientID {
			return true
		}
	}
	return false
}
