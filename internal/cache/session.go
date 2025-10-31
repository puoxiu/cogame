package cache

import (
	"encoding/json"
	"fmt"
	"time"
)

// SessionCache 会话缓存
type SessionCache struct {
	redis  *RedisManager
	prefix string
	expiry time.Duration
}

// NewSessionCache 创建会话缓存
func NewSessionCache(redis *RedisManager) *SessionCache {
	return &SessionCache{
		redis:  redis,
		prefix: "session:",
		expiry: 2 * time.Hour,
	}
}


// SetSession 设置会话
func (sc *SessionCache) SetSession(sessionID string, userID uint64) error {
	key := fmt.Sprintf("%s%s", sc.prefix, sessionID)
	return sc.redis.Set(key, userID, sc.expiry)
}

// GetSession 获取会话
func (sc *SessionCache) GetSession(sessionID string) (uint64, error) {
	key := fmt.Sprintf("%s%s", sc.prefix, sessionID)
	result, err := sc.redis.GetString(key)
	if err != nil {
		return 0, err
	}

	var userID uint64
	if err := json.Unmarshal([]byte(result), &userID); err != nil {
		return 0, err
	}

	return userID, nil
}

// DeleteSession 删除会话
func (sc *SessionCache) DeleteSession(sessionID string) error {
	key := fmt.Sprintf("%s%s", sc.prefix, sessionID)
	return sc.redis.Delete(key)
}

// RefreshSession 刷新会话
func (sc *SessionCache) RefreshSession(sessionID string) error {
	key := fmt.Sprintf("%s%s", sc.prefix, sessionID)
	return sc.redis.Expire(key, sc.expiry)
}



