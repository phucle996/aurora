package storageDto

// CreateCredentialRequest đại diện cho payload yêu cầu tạo Access Key mới cho bucket cá nhân.
type CreateCredentialRequest struct {
	Policy string `json:"policy" binding:"required"`
}

// CreateTenantCredentialRequest đại diện cho payload yêu cầu tạo Access Key mới cho bucket doanh nghiệp.
type CreateTenantCredentialRequest struct {
	Policy string `json:"policy"`
}

// DeleteCredentialRequest đại diện cho payload yêu cầu xóa Access Key.
type DeleteCredentialRequest struct {
	AccessKey string `json:"access_key" binding:"required"`
}

// DeleteTenantCredentialRequest đại diện cho payload yêu cầu xóa Access Key của bucket doanh nghiệp.
type DeleteTenantCredentialRequest struct {
	AccessKey string `json:"access_key"`
}
