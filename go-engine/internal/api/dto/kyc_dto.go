package dto

type KYCSubmitRequest struct {
	UserID               string `json:"user_id" binding:"required"`
	FirstName            string `json:"first_name" binding:"required"`
	LastName             string `json:"last_name" binding:"required"`
	DateOfBirth          string `json:"date_of_birth" binding:"required"`
	PhoneNumber          string `json:"phone_number" binding:"required"`
	NIN                  string `json:"nin" binding:"required,len=11,numeric"`
	BVN                  string `json:"bvn" binding:"required,len=11,numeric"`
	Tier                 string `json:"tier,omitempty"`
	SelfieBase64         string `json:"selfie_base64,omitempty"`
	ProofOfAddressBase64 string `json:"proof_of_address_base64,omitempty"`
}

type KYCStatusResponse struct {
	UserID          string `json:"user_id"`
	Status          string `json:"status"`
	KYCStatus       string `json:"kyc_status"`
	Provider        string `json:"provider,omitempty"`
	Tier            string `json:"tier,omitempty"`
	SubmittedAt     string `json:"submitted_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}
