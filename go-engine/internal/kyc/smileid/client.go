package smileid

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	BaseURLProduction = "https://api.smileidentity.com/v2"
	BaseURLSandbox    = "https://testapi.smileidentity.com/v2"

	basicKYCJobType          = 5
	iso8601MillisLayout      = "2006-01-02T15:04:05.000Z"
	basicKYCSourceSDK        = "rest_api"
	basicKYCSourceSDKVersion = "convertchain-go-engine"
)

type Client struct {
	partnerID  string
	apiKey     string
	baseURL    string
	httpClient *http.Client
	sandbox    bool
}

func NewClient(partnerID, apiKey string, sandbox bool) *Client {
	baseURL := BaseURLProduction
	if sandbox {
		baseURL = BaseURLSandbox
	}

	return &Client{
		partnerID: strings.TrimSpace(partnerID),
		apiKey:    strings.TrimSpace(apiKey),
		baseURL:   baseURL,
		sandbox:   sandbox,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.partnerID != "" && c.apiKey != ""
}

func (c *Client) IsSandbox() bool {
	return c != nil && c.sandbox
}

func (c *Client) VerifyCallbackSignature(signature string, timestamp string) bool {
	if !c.Enabled() {
		return false
	}

	normalizedSignature := strings.TrimSpace(signature)
	normalizedTimestamp := strings.TrimSpace(timestamp)
	if normalizedSignature == "" || normalizedTimestamp == "" {
		return false
	}

	expected := c.generateSignature(normalizedTimestamp)
	return hmac.Equal([]byte(normalizedSignature), []byte(expected))
}

func (c *Client) generateSignature(timestamp string) string {
	message := timestamp + c.partnerID + "sid_request"
	mac := hmac.New(sha256.New, []byte(c.apiKey))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type BVNLookupRequest struct {
	BVN         string
	FirstName   string
	MiddleName  string
	LastName    string
	DateOfBirth string
	PhoneNumber string
}

type BVNLookupResult struct {
	FullName    string
	DateOfBirth string
	PhoneNumber string
	Status      string
	Reason      string
	ResultCode  string
	ResultText  string
	Verified    bool
	NameMatch   bool
	DOBMatch    bool
	PhoneMatch  bool
}

func (c *Client) LookupBVN(ctx context.Context, req BVNLookupRequest) (*BVNLookupResult, error) {
	result, err := c.verifyBasicKYC(ctx, basicKYCRequest{
		IDType:      "BVN",
		IDNumber:    req.BVN,
		FirstName:   req.FirstName,
		MiddleName:  req.MiddleName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		PhoneNumber: req.PhoneNumber,
		UserID:      "bvn:" + req.BVN,
		JobID:       fmt.Sprintf("bvn-%s-%d", strings.ReplaceAll(req.BVN, " ", ""), time.Now().UTC().UnixNano()),
	})
	if err != nil {
		return nil, err
	}

	status, reason, verified := classifyBasicKYCResult(result)
	nameMatch := actionMatches(result.Actions.Names)
	if !nameMatch && fullNameMatches(req.FirstName, req.MiddleName, req.LastName, result.FullName) {
		nameMatch = true
	}
	dobMatch := actionMatches(result.Actions.DOB)
	if !dobMatch && dateMatches(req.DateOfBirth, result.DateOfBirth) {
		dobMatch = true
	}
	phoneMatch := actionMatches(result.Actions.PhoneNumber)
	if !phoneMatch && phoneNumbersMatch(req.PhoneNumber, result.PhoneNumber) {
		phoneMatch = true
	}

	return &BVNLookupResult{
		FullName:    result.FullName,
		DateOfBirth: result.DateOfBirth,
		PhoneNumber: result.PhoneNumber,
		Status:      status,
		Reason:      reason,
		ResultCode:  strings.TrimSpace(result.ResultCode),
		ResultText:  strings.TrimSpace(result.ResultText),
		Verified:    verified,
		NameMatch:   nameMatch,
		DOBMatch:    dobMatch,
		PhoneMatch:  phoneMatch,
	}, nil
}

type NINLookupRequest struct {
	NIN         string
	FirstName   string
	MiddleName  string
	LastName    string
	DateOfBirth string
	PhoneNumber string
}

type NINLookupResult struct {
	Status      string
	Reason      string
	FullName    string
	DateOfBirth string
	PhoneNumber string
	ResultCode  string
	ResultText  string
	Verified    bool
	NameMatch   bool
	DOBMatch    bool
	PhoneMatch  bool
}

func (c *Client) LookupNIN(ctx context.Context, req NINLookupRequest) (*NINLookupResult, error) {
	result, err := c.verifyBasicKYC(ctx, basicKYCRequest{
		IDType:      "NIN_SLIP",
		IDNumber:    req.NIN,
		FirstName:   req.FirstName,
		MiddleName:  req.MiddleName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		PhoneNumber: req.PhoneNumber,
		UserID:      "nin:" + req.NIN,
		JobID:       fmt.Sprintf("nin-%s-%d", strings.ReplaceAll(req.NIN, " ", ""), time.Now().UTC().UnixNano()),
	})
	if err != nil {
		return nil, err
	}

	status, reason, verified := classifyBasicKYCResult(result)
	nameMatch := actionMatches(result.Actions.Names)
	if !nameMatch && fullNameMatches(req.FirstName, req.MiddleName, req.LastName, result.FullName) {
		nameMatch = true
	}
	dobMatch := actionMatches(result.Actions.DOB)
	if !dobMatch && dateMatches(req.DateOfBirth, result.DateOfBirth) {
		dobMatch = true
	}
	phoneMatch := actionMatches(result.Actions.PhoneNumber)
	if !phoneMatch && phoneNumbersMatch(req.PhoneNumber, result.PhoneNumber) {
		phoneMatch = true
	}

	return &NINLookupResult{
		Status:      status,
		Reason:      reason,
		FullName:    result.FullName,
		DateOfBirth: result.DateOfBirth,
		PhoneNumber: result.PhoneNumber,
		ResultCode:  strings.TrimSpace(result.ResultCode),
		ResultText:  strings.TrimSpace(result.ResultText),
		Verified:    verified,
		NameMatch:   nameMatch,
		DOBMatch:    dobMatch,
		PhoneMatch:  phoneMatch,
	}, nil
}

type basicKYCRequest struct {
	IDType      string
	IDNumber    string
	FirstName   string
	MiddleName  string
	LastName    string
	DateOfBirth string
	PhoneNumber string
	UserID      string
	JobID       string
}

type basicKYCResponse struct {
	FullName    string `json:"FullName"`
	DateOfBirth string `json:"DOB"`
	PhoneNumber string `json:"PhoneNumber"`
	ResultCode  string `json:"ResultCode"`
	ResultText  string `json:"ResultText"`
	Actions     struct {
		ReturnPersonalInfo string `json:"Return_Personal_Info"`
		VerifyIDNumber     string `json:"Verify_ID_Number"`
		Names              string `json:"Names"`
		DOB                string `json:"DOB"`
		PhoneNumber        string `json:"Phone_Number"`
		IDVerification     string `json:"ID_Verification"`
	} `json:"Actions"`
}

func (c *Client) verifyBasicKYC(ctx context.Context, req basicKYCRequest) (*basicKYCResponse, error) {
	timestamp := time.Now().UTC().Format(iso8601MillisLayout)
	partnerParams := map[string]any{
		"job_id":   strings.TrimSpace(req.JobID),
		"user_id":  strings.TrimSpace(req.UserID),
		"job_type": basicKYCJobType,
	}
	payload := map[string]any{
		"source_sdk":         basicKYCSourceSDK,
		"source_sdk_version": basicKYCSourceSDKVersion,
		"partner_id":         c.partnerID,
		"timestamp":          timestamp,
		"signature":          c.generateSignature(timestamp),
		"country":            "NG",
		"id_type":            strings.TrimSpace(req.IDType),
		"id_number":          strings.TrimSpace(req.IDNumber),
		"first_name":         strings.TrimSpace(req.FirstName),
		"last_name":          strings.TrimSpace(req.LastName),
		"partner_params":     partnerParams,
	}
	if middleName := strings.TrimSpace(req.MiddleName); middleName != "" {
		payload["middle_name"] = middleName
	}
	if dob := strings.TrimSpace(req.DateOfBirth); dob != "" {
		payload["dob"] = dob
	}
	if phone := strings.TrimSpace(req.PhoneNumber); phone != "" {
		payload["phone_number"] = phone
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/verify",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SmileID API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	result := &basicKYCResponse{}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return nil, fmt.Errorf("failed to decode SmileID response: %w", err)
	}

	return result, nil
}

func classifyBasicKYCResult(result *basicKYCResponse) (string, string, bool) {
	if result == nil {
		return "UNKNOWN", "Empty SmileID response", false
	}

	resultCode := strings.TrimSpace(result.ResultCode)
	verifyID := strings.TrimSpace(result.Actions.VerifyIDNumber)
	resultText := strings.TrimSpace(result.ResultText)

	switch resultCode {
	case "1012", "1020", "1021", "1022":
		if strings.EqualFold(verifyID, "Verified") || resultCode == "1012" {
			return "VALID", "", true
		}
	case "1013":
		return "NOT_FOUND", firstNonEmptyString(resultText, "ID details were not found in the authority database."), false
	case "1014":
		return "INVALID", firstNonEmptyString(resultText, "ID number format is invalid."), false
	case "1015":
		return "UNAVAILABLE", firstNonEmptyString(resultText, "The identity issuer is unavailable right now."), false
	case "1016":
		return "UNSUPPORTED", firstNonEmptyString(resultText, "This ID type is not enabled for the configured SmileID account."), false
	}

	switch {
	case strings.EqualFold(verifyID, "Verified"):
		return "VALID", "", true
	case strings.EqualFold(verifyID, "Issuer Unavailable"), strings.EqualFold(verifyID, "Not Done"):
		return "UNAVAILABLE", firstNonEmptyString(resultText, "The identity issuer is unavailable right now."), false
	default:
		return "NOT_FOUND", firstNonEmptyString(resultText, "ID details were not found in the authority database."), false
	}
}

func actionMatches(action string) bool {
	switch strings.TrimSpace(action) {
	case "Exact Match", "Partial Match", "Transposed":
		return true
	default:
		return false
	}
}

func fullNameMatches(firstName, middleName, lastName, fullName string) bool {
	expected := normalizeName(strings.TrimSpace(strings.Join([]string{firstName, middleName, lastName}, " ")))
	actual := normalizeName(fullName)
	return expected != "" && expected == actual
}

func normalizeName(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func dateMatches(expected, actual string) bool {
	return strings.TrimSpace(expected) != "" && strings.TrimSpace(expected) == strings.TrimSpace(actual)
}

func phoneNumbersMatch(expected, actual string) bool {
	expectedDigits := normalizeDigits(expected)
	actualDigits := normalizeDigits(actual)
	if expectedDigits == "" || actualDigits == "" {
		return false
	}
	if expectedDigits == actualDigits {
		return true
	}
	if len(expectedDigits) >= 10 && len(actualDigits) >= 10 {
		return expectedDigits[len(expectedDigits)-10:] == actualDigits[len(actualDigits)-10:]
	}
	return false
}

func normalizeDigits(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
