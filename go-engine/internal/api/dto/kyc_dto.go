package dto

type KYCSubmitRequest struct {
	UserID      string `json:"user_id" binding:"required"`
	FullName    string `json:"full_name" binding:"required"`
	DateOfBirth string `json:"date_of_birth" binding:"required"`
	IDType      string `json:"id_type" binding:"required,oneof=BVN NIN"`
	IDNumber    string `json:"id_number" binding:"required"`
	PhoneNumber string `json:"phone_number"`
}

type KYCStatusResponse struct {
	UserID          string `json:"user_id"`
	Status          string `json:"status"`
	Provider        string `json:"provider"`
	SubmittedAt     string `json:"submitted_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}