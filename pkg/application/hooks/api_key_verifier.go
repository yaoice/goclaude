// Package hooks 应用层 hook 实现。
// 提供 API key 验证器、会话管理、重连管理等 hook 的具体实现。
package hooks

import (
	"errors"
	"sync"

	"github.com/anthropics/goclaude/pkg/domain/hook"
)

// APIKeyVerifierFunc API key 验证函数类型
// 接收 apiKey，返回 (isValid, error)。
// 对齐 src/services/api/claude.ts:verifyApiKey 的行为。
type APIKeyVerifierFunc func(apiKey string) (bool, error)

// APIKeySource API key 来源
type APIKeySource string

const (
	APIKeySourceEnv    APIKeySource = "env"
	APIKeySourceConfig APIKeySource = "config"
	APIKeySourceHelper APIKeySource = "apiKeyHelper"
	APIKeySourceNone   APIKeySource = "none"
)

// APIKeyProvider API key 获取接口
// 对齐 src/utils/auth.ts 中的 getAnthropicApiKeyWithSource / getApiKeyFromApiKeyHelper。
type APIKeyProvider interface {
	// GetKey 获取 API key 及其来源（不执行 apiKeyHelper）
	GetKey() (key string, source APIKeySource)
	// GetKeyFromHelper 从 apiKeyHelper 获取 key（预热缓存）
	GetKeyFromHelper(nonInteractive bool) (string, error)
}

// apiKeyVerifier APIKeyVerifier 实现。
//
// 核心逻辑对齐 src/hooks/useApiKeyVerification.ts:useApiKeyVerification：
//  1. 若 auth 未启用或用户是 subscriber → valid（跳过验证）
//  2. 初始化时跳过 apiKeyHelper 执行（安全：防止 settings.json RCE）
//  3. 若无 key 且无 apiKeyHelper → missing
//  4. 若有 key 或 apiKeyHelper 已配置 → loading（等待后续验证）
//  5. 调用 verifyApiKey 验证 → valid / invalid / error
type apiKeyVerifier struct {
	isAuthEnabled func() bool
	isSubscriber  func() bool
	keyProvider   APIKeyProvider
	verifierFunc  APIKeyVerifierFunc

	mu          sync.RWMutex
	status      hook.VerificationStatus
	lastErr     error
	initialized bool
}

// NewAPIKeyVerifier 创建 API key 验证器
func NewAPIKeyVerifier(
	isAuthEnabled func() bool,
	isSubscriber func() bool,
	keyProvider APIKeyProvider,
	verifierFunc APIKeyVerifierFunc,
) hook.APIKeyVerifier {
	v := &apiKeyVerifier{
		isAuthEnabled: isAuthEnabled,
		isSubscriber:  isSubscriber,
		keyProvider:   keyProvider,
		verifierFunc:  verifierFunc,
	}
	v.initStatus()
	return v
}

// initStatus 初始化状态（对齐 TS 的 useState 惰性初始化）
//
// 对齐逻辑：
//   if (!isAnthropicAuthEnabled() || isClaudeAISubscriber()) return 'valid'
//   const { key, source } = getAnthropicApiKeyWithSource({ skipRetrievingKeyFromApiKeyHelper: true })
//   if (key || source === 'apiKeyHelper') return 'loading'
//   return 'missing'
func (v *apiKeyVerifier) initStatus() {
	if !v.isAuthEnabled() || v.isSubscriber() {
		v.status = hook.VerificationValid
		return
	}
	// 注意：GetKey() 不执行 apiKeyHelper（等同于 skipRetrievingKeyFromApiKeyHelper: true）
	key, source := v.keyProvider.GetKey()
	if key != "" || source == APIKeySourceHelper {
		v.status = hook.VerificationLoading
		return
	}
	v.status = hook.VerificationMissing
	v.initialized = true
}

// Status 返回当前验证状态（线程安全）
func (v *apiKeyVerifier) Status() hook.VerificationStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.status
}

// LastError 返回最后一次错误（线程安全）
func (v *apiKeyVerifier) LastError() error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastErr
}

// Verify 执行 API key 验证
func (v *apiKeyVerifier) Verify() (hook.VerificationStatus, error) {
	return v.verify(false)
}

// Reverify 重新验证 API key
func (v *apiKeyVerifier) Reverify() (hook.VerificationStatus, error) {
	return v.verify(true)
}

// verify 核心验证逻辑。
//
// 对齐 TS 的 verify callback：
//   1. auth 未启用 / subscriber → valid
//   2. 预热 apiKeyHelper 缓存 → getApiKeyFromApiKeyHelper(getIsNonInteractiveSession())
//   3. 获取 key → getAnthropicApiKeyWithSource()
//   4. 无 key：
//      - source === 'apiKeyHelper' → error（helper 未返回有效 key）
//      - 其他 → missing
//   5. 调用 verifyApiKey(key, false) → valid / invalid
//   6. 捕获异常 → error（非认证错误的 API 错误）
func (v *apiKeyVerifier) verify(_ bool) (hook.VerificationStatus, error) {
	// 步骤 1: 如果 auth 未启用或用户是 subscriber，直接返回 valid
	if !v.isAuthEnabled() || v.isSubscriber() {
		v.mu.Lock()
		v.status = hook.VerificationValid
		v.lastErr = nil
		v.mu.Unlock()
		return hook.VerificationValid, nil
	}

	// 步骤 2: 预热 apiKeyHelper 缓存（对齐 TS: await getApiKeyFromApiKeyHelper(getIsNonInteractiveSession())）
	if _, err := v.keyProvider.GetKeyFromHelper(false); err != nil {
		v.mu.Lock()
		v.status = hook.VerificationError
		v.lastErr = err
		v.mu.Unlock()
		return hook.VerificationError, err
	}

	// 步骤 3: 从所有来源读取 key（对齐 TS: const { key: apiKey, source } = getAnthropicApiKeyWithSource()）
	key, source := v.keyProvider.GetKey()

	// 步骤 4: 无 key 的处理
	if key == "" {
		v.mu.Lock()
		defer v.mu.Unlock()
		if source == APIKeySourceHelper {
			// 对齐 TS: if (source === 'apiKeyHelper') { setStatus('error'); setError(new Error(...)) }
			v.status = hook.VerificationError
			v.lastErr = errors.New("API key helper did not return a valid key")
		} else {
			// 对齐 TS: const newStatus = 'missing'; setStatus(newStatus)
			v.status = hook.VerificationMissing
			v.lastErr = nil
		}
		return v.status, v.lastErr
	}

	// 步骤 5-6: 验证 key
	isValid, err := v.verifierFunc(key)
	v.mu.Lock()
	defer v.mu.Unlock()

	if err != nil {
		// 对齐 TS catch 分支：
		//   setError(error as Error); setStatus('error')
		// 非认证错误的 API 错误（如网络错误、500 等）
		v.status = hook.VerificationError
		v.lastErr = err
		return v.status, err
	}

	if isValid {
		v.status = hook.VerificationValid
	} else {
		v.status = hook.VerificationInvalid
	}
	v.lastErr = nil
	return v.status, nil
}
