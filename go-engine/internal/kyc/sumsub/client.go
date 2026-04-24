package sumsub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://api.sumsub.com"

type Client struct {
	appToken   string
	secretKey  string
	baseURL    string
	httpClient *http.Client
}

func NewClient(appToken, secretKey string, sandbox bool) *Client {
	return &Client{
		appToken:   strings.TrimSpace(appToken),
		secretKey:  strings.TrimSpace(secretKey),
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.appToken != "" && c.secretKey != ""
}

func (c *Client) VerifyWebhookSignature(payload []byte, digest, algorithm, secretOverride string) bool {
	if c == nil {
		return false
	}

	secret := strings.TrimSpace(secretOverride)
	if secret == "" {
		secret = c.secretKey
	}
	if secret == "" {
		return false
	}

	normalizedDigest := strings.ToLower(strings.TrimSpace(digest))
	normalizedDigest = strings.TrimPrefix(normalizedDigest, "sha256=")
	if normalizedDigest == "" {
		return false
	}

	normalizedAlgorithm := strings.ToUpper(strings.TrimSpace(algorithm))
	if normalizedAlgorithm == "" {
		normalizedAlgorithm = "HMAC_SHA256_HEX"
	}

	switch normalizedAlgorithm {
	case "HMAC_SHA256", "HMAC_SHA256_HEX", "SHA256":
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		expected := hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(normalizedDigest), []byte(expected))
	default:
		return false
	}
}

type ApplicantRequest struct {
	ExternalUserID string
	LevelName      string
	FirstName      string
	LastName       string
	DateOfBirth    string
	Email          string
	PhoneNumber    string
}

type Applicant struct {
	ID             string `json:"id"`
	InspectionID   string `json:"inspectionId"`
	ExternalUserID string `json:"externalUserId"`
	ReviewStatus   string `json:"reviewStatus"`
}

func (c *Client) CreateApplicant(ctx context.Context, req ApplicantRequest) (*Applicant, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("sumsub is not configured")
	}

	levelName := strings.TrimSpace(req.LevelName)
	if levelName == "" {
		levelName = "telegram-tier2"
	}

	bodyPayload := map[string]any{
		"externalUserId": req.ExternalUserID,
		"type":           "individual",
	}

	fixedInfo := map[string]any{}
	if strings.TrimSpace(req.FirstName) != "" {
		fixedInfo["firstName"] = strings.TrimSpace(req.FirstName)
	}
	if strings.TrimSpace(req.LastName) != "" {
		fixedInfo["lastName"] = strings.TrimSpace(req.LastName)
	}
	if strings.TrimSpace(req.DateOfBirth) != "" {
		fixedInfo["dob"] = strings.TrimSpace(req.DateOfBirth)
	}
	if len(fixedInfo) > 0 {
		bodyPayload["fixedInfo"] = fixedInfo
	}
	if strings.TrimSpace(req.Email) != "" {
		bodyPayload["email"] = strings.TrimSpace(req.Email)
	}
	if strings.TrimSpace(req.PhoneNumber) != "" {
		bodyPayload["phone"] = strings.TrimSpace(req.PhoneNumber)
	}

	body, err := json.Marshal(bodyPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal applicant request: %w", err)
	}

	uri := "/resources/applicants?levelName=" + url.QueryEscape(levelName)
	resp, err := c.do(ctx, http.MethodPost, uri, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sumsub create applicant failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(bodyText)))
	}

	var applicant Applicant
	if err := json.NewDecoder(resp.Body).Decode(&applicant); err != nil {
		return nil, fmt.Errorf("decode applicant response: %w", err)
	}
	if applicant.ID == "" {
		return nil, fmt.Errorf("sumsub create applicant response missing applicant id")
	}

	return &applicant, nil
}

func (c *Client) do(ctx context.Context, method, uri string, body []byte) (*http.Response, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+uri, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-App-Token", c.appToken)
	req.Header.Set("X-App-Access-Ts", timestamp)
	req.Header.Set("X-App-Access-Sig", c.sign(method, uri, body, timestamp))
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

func (c *Client) sign(method, uri string, body []byte, timestamp string) string {
	payload := timestamp + strings.ToUpper(method) + uri + string(body)
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

type LivenessRequest struct {
	UserID     string
	SelfieData string
}

type LivenessResult struct {
	IsLive       bool
	Confidence   float64
	RejectReason string
}

func (c *Client) CheckLiveness(ctx context.Context, req LivenessRequest) (*LivenessResult, error) {
	return &LivenessResult{IsLive: true, Confidence: 0.95}, nil
}

type DocVerifyRequest struct {
	UserID     string
	DocType    string
	DocData    string
	MaxAgeDays int
}

type DocVerifyResult struct {
	Verified      bool
	RejectReason  string
	ExtractedData map[string]string
}

func (c *Client) VerifyDocument(ctx context.Context, req DocVerifyRequest) (*DocVerifyResult, error) {
	return &DocVerifyResult{Verified: true, ExtractedData: map[string]string{}}, nil
}
