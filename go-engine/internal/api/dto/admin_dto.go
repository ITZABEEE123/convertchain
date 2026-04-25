package dto

type AdminDisputeResponse struct {
	DisputeID      string `json:"dispute_id"`
	TicketRef      string `json:"ticket_ref"`
	TradeID        string `json:"trade_id"`
	TradeRef       string `json:"trade_ref"`
	UserID         string `json:"user_id"`
	Source         string `json:"source"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	ResolutionMode string `json:"resolution_mode,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
	Resolver       string `json:"resolver,omitempty"`
	CreatedAt      string `json:"created_at"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

type AdminDisputeListResponse struct {
	Disputes []AdminDisputeResponse `json:"disputes"`
}

type ResolveDisputeRequest struct {
	ResolutionMode string `json:"resolution_mode" binding:"required,oneof=retry_processing close_no_payout force_complete"`
	ResolutionNote string `json:"resolution_note"`
	Resolver       string `json:"resolver"`
}

type ProviderReadinessCheckResponse struct {
	Enabled bool                   `json:"enabled"`
	Healthy bool                   `json:"healthy"`
	Summary string                 `json:"summary"`
	Details map[string]interface{} `json:"details,omitempty"`
}

type ProviderReadinessResponse struct {
	GeneratedAt    string                         `json:"generated_at"`
	OverallHealthy bool                           `json:"overall_healthy"`
	Graph          ProviderReadinessCheckResponse `json:"graph"`
	Binance        ProviderReadinessCheckResponse `json:"binance"`
	Bybit          ProviderReadinessCheckResponse `json:"bybit"`
	SmileID        ProviderReadinessCheckResponse `json:"smileid"`
	Sumsub         ProviderReadinessCheckResponse `json:"sumsub"`
}

type AMLCaseResponse struct {
	CaseID              string                 `json:"case_id"`
	CaseRef             string                 `json:"case_ref"`
	UserID              string                 `json:"user_id,omitempty"`
	TradeID             string                 `json:"trade_id,omitempty"`
	QuoteID             string                 `json:"quote_id,omitempty"`
	CaseType            string                 `json:"case_type"`
	Severity            string                 `json:"severity"`
	Status              string                 `json:"status"`
	Reason              string                 `json:"reason"`
	Evidence            map[string]interface{} `json:"evidence,omitempty"`
	Disposition         string                 `json:"disposition,omitempty"`
	DispositionNote     string                 `json:"disposition_note,omitempty"`
	STRReferralMetadata map[string]interface{} `json:"str_referral_metadata,omitempty"`
	AssignedTo          string                 `json:"assigned_to,omitempty"`
	CreatedAt           string                 `json:"created_at"`
	UpdatedAt           string                 `json:"updated_at"`
	ResolvedAt          string                 `json:"resolved_at,omitempty"`
}

type AMLCaseListResponse struct {
	Cases []AMLCaseResponse `json:"cases"`
}

type AMLCaseDispositionRequest struct {
	Status              string                 `json:"status" binding:"required,oneof=IN_REVIEW ESCALATED DISMISSED CONFIRMED REFERRED_STR CLOSED"`
	Disposition         string                 `json:"disposition"`
	DispositionNote     string                 `json:"disposition_note"`
	Actor               string                 `json:"actor"`
	STRReferralMetadata map[string]interface{} `json:"str_referral_metadata"`
}

type LegalLaunchApprovalRequest struct {
	Environment    string                 `json:"environment" binding:"required"`
	ApprovalStatus string                 `json:"approval_status" binding:"required,oneof=PENDING APPROVED REJECTED REVOKED"`
	ApprovedBy     string                 `json:"approved_by"`
	LegalMemoRef   string                 `json:"legal_memo_ref"`
	Notes          string                 `json:"notes"`
	Evidence       map[string]interface{} `json:"evidence"`
	SignedAt       string                 `json:"signed_at"`
}

type LegalLaunchApprovalResponse struct {
	ApprovalID     string                 `json:"approval_id"`
	Environment    string                 `json:"environment"`
	ApprovalStatus string                 `json:"approval_status"`
	ApprovedBy     string                 `json:"approved_by,omitempty"`
	LegalMemoRef   string                 `json:"legal_memo_ref,omitempty"`
	Notes          string                 `json:"notes,omitempty"`
	Evidence       map[string]interface{} `json:"evidence,omitempty"`
	SignedAt       string                 `json:"signed_at,omitempty"`
	CreatedAt      string                 `json:"created_at"`
	UpdatedAt      string                 `json:"updated_at"`
}

type DataProtectionEventRequest struct {
	UserID    string                 `json:"user_id"`
	EventType string                 `json:"event_type" binding:"required,oneof=LAWFUL_BASIS_RECORDED RETENTION_POLICY_RECORDED DSAR_REQUESTED DSAR_FULFILLED ERASURE_REQUESTED ERASURE_COMPLETED ANONYMIZATION_COMPLETED BREACH_INCIDENT_RECORDED BREACH_RESPONSE_RECORDED"`
	Status    string                 `json:"status" binding:"required,oneof=OPEN IN_PROGRESS COMPLETED REJECTED"`
	Reference string                 `json:"reference"`
	Details   map[string]interface{} `json:"details"`
	CreatedBy string                 `json:"created_by"`
}

type DataProtectionEventResponse struct {
	EventID   string                 `json:"event_id"`
	UserID    string                 `json:"user_id,omitempty"`
	EventType string                 `json:"event_type"`
	Status    string                 `json:"status"`
	Reference string                 `json:"reference,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
	CreatedBy string                 `json:"created_by,omitempty"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
}
