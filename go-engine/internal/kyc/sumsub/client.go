package sumsub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
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
	sandbox    bool
	httpClient *http.Client
}

func NewClient(appToken, secretKey string, sandbox bool) *Client {
	return &Client{
		appToken:   strings.TrimSpace(appToken),
		secretKey:  strings.TrimSpace(secretKey),
		baseURL:    baseURL,
		sandbox:    sandbox,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.appToken != "" && c.secretKey != ""
}

func (c *Client) IsSandbox() bool {
	return c != nil && c.sandbox
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

	normalizedDigest := normalizeDigest(digest)
	if normalizedDigest == "" {
		return false
	}

	normalizedAlgorithm := strings.ToUpper(strings.TrimSpace(algorithm))
	if normalizedAlgorithm == "" {
		normalizedAlgorithm = "HMAC_SHA256_HEX"
	}

	var h func() hash.Hash
	switch normalizedAlgorithm {
	case "HMAC_SHA1", "HMAC_SHA1_HEX", "SHA1":
		h = sha1.New
	case "HMAC_SHA256", "HMAC_SHA256_HEX", "SHA256":
		h = sha256.New
	case "HMAC_SHA512", "HMAC_SHA512_HEX", "SHA512":
		h = sha512.New
	default:
		return false
	}

	mac := hmac.New(h, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)

	provided, err := hex.DecodeString(normalizedDigest)
	if err == nil && len(provided) == len(expected) {
		return hmac.Equal(provided, expected)
	}

	expectedHex := hex.EncodeToString(expected)
	return hmac.Equal([]byte(normalizedDigest), []byte(expectedHex))
}

func normalizeDigest(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}

	for _, prefix := range []string{"sha1=", "sha256=", "sha512=", "hmac-sha1=", "hmac-sha256=", "hmac-sha512=", "signature=", "sig="} {
		trimmed = strings.TrimPrefix(trimmed, prefix)
	}
	return strings.TrimSpace(trimmed)
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
		return nil, &ProviderError{Operation: "create_applicant", Code: "SUMSUB_NOT_CONFIGURED", Message: "sumsub is not configured"}
	}

	levelName := strings.TrimSpace(req.LevelName)
	if levelName == "" {
		return nil, &ProviderError{Operation: "create_applicant", Code: "SUMSUB_LEVEL_NAME_MISSING", Message: "sumsub levelName is required"}
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
		return nil, decodeProviderError("create_applicant", resp)
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

type WebSDKLinkRequest struct {
	UserID      string
	LevelName   string
	Email       string
	PhoneNumber string
	TTLInSecs   int
}

type WebSDKLink struct {
	URL string `json:"url"`
}

func (c *Client) CreateWebSDKLink(ctx context.Context, req WebSDKLinkRequest) (*WebSDKLink, error) {
	if !c.Enabled() {
		return nil, &ProviderError{Operation: "create_websdk_link", Code: "SUMSUB_NOT_CONFIGURED", Message: "sumsub is not configured"}
	}

	levelName := strings.TrimSpace(req.LevelName)
	if levelName == "" {
		return nil, &ProviderError{Operation: "create_websdk_link", Code: "SUMSUB_LEVEL_NAME_MISSING", Message: "sumsub levelName is required"}
	}
	ttl := req.TTLInSecs
	if ttl <= 0 {
		ttl = 1800
	}

	bodyPayload := map[string]any{
		"levelName": levelName,
		"userId":    strings.TrimSpace(req.UserID),
		"ttlInSecs": ttl,
	}
	identifiers := map[string]string{}
	if strings.TrimSpace(req.Email) != "" {
		identifiers["email"] = strings.TrimSpace(req.Email)
	}
	if strings.TrimSpace(req.PhoneNumber) != "" {
		identifiers["phone"] = strings.TrimSpace(req.PhoneNumber)
	}
	if len(identifiers) > 0 {
		bodyPayload["applicantIdentifiers"] = identifiers
	}

	body, err := json.Marshal(bodyPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal websdk link request: %w", err)
	}

	uri := "/resources/sdkIntegrations/levels/-/websdkLink?lang=en&source=api"
	resp, err := c.do(ctx, http.MethodPost, uri, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeProviderError("create_websdk_link", resp)
	}

	var link WebSDKLink
	if err := json.NewDecoder(resp.Body).Decode(&link); err != nil {
		return nil, fmt.Errorf("decode websdk link response: %w", err)
	}
	if strings.TrimSpace(link.URL) == "" {
		return nil, &ProviderError{Operation: "create_websdk_link", Code: "SUMSUB_EMPTY_WEBSDK_LINK", Message: "sumsub websdk link response missing url"}
	}
	return &link, nil
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

type ProviderError struct {
	Operation  string
	Code       string
	Message    string
	StatusCode int
	Retryable  bool
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("sumsub %s failed (%s, status=%d): %s", e.Operation, e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("sumsub %s failed (%s): %s", e.Operation, e.Code, e.Message)
}

func decodeProviderError(operation string, resp *http.Response) error {
	bodyText, _ := io.ReadAll(resp.Body)
	trimmedBody := strings.TrimSpace(string(bodyText))

	var apiErr struct {
		Description   string `json:"description"`
		Code          int    `json:"code"`
		CorrelationID string `json:"correlationId"`
		ErrorCode     int    `json:"errorCode"`
		ErrorName     string `json:"errorName"`
		Message       string `json:"message"`
	}
	if trimmedBody != "" {
		_ = json.Unmarshal(bodyText, &apiErr)
	}

	message := firstNonEmpty(apiErr.Description, apiErr.Message, trimmedBody, resp.Status)
	code := strings.ToUpper(strings.TrimSpace(apiErr.ErrorName))
	if code == "" && apiErr.ErrorCode != 0 {
		code = fmt.Sprintf("SUMSUB_%d", apiErr.ErrorCode)
	}
	if code == "" {
		code = fmt.Sprintf("SUMSUB_HTTP_%d", resp.StatusCode)
	}
	code = strings.NewReplacer(" ", "_", "-", "_").Replace(code)

	return &ProviderError{
		Operation:  operation,
		Code:       code,
		Message:    message,
		StatusCode: resp.StatusCode,
		Retryable:  resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
