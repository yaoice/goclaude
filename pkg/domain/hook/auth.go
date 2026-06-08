// Package hook domain models for authentication hooks.
// Aligns with src/hooks/useApiKeyVerification.ts verification status flow.
package hook

import "fmt"

// VerificationStatus API key 验证状态
type VerificationStatus string

const (
	VerificationLoading VerificationStatus = "loading"
	VerificationValid   VerificationStatus = "valid"
	VerificationInvalid VerificationStatus = "invalid"
	VerificationMissing VerificationStatus = "missing"
	VerificationError   VerificationStatus = "error"
)

// APIKeyVerifier API key 验证器接口
type APIKeyVerifier interface {
	Verify() (status VerificationStatus, err error)
	Reverify() (status VerificationStatus, err error)
	Status() VerificationStatus
	LastError() error
}

// APIKeyVerificationResult 验证结果（含错误信息）
type APIKeyVerificationResult struct {
	Status VerificationStatus
	Error  error
}

// String 友好输出
func (s VerificationStatus) String() string { return string(s) }

// IsTerminal 判断是否为终态（不再需要轮询）
func (s VerificationStatus) IsTerminal() bool {
	return s == VerificationValid || s == VerificationInvalid || s == VerificationMissing || s == VerificationError
}

// APIKeyVerificationError 验证错误包装
type APIKeyVerificationError struct {
	Status VerificationStatus
	Err    error
}

func (e *APIKeyVerificationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("api key verification [%s]: %v", e.Status, e.Err)
	}
	return fmt.Sprintf("api key verification [%s]", e.Status)
}

func (e *APIKeyVerificationError) Unwrap() error { return e.Err }
