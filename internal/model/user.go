package model

import "time"




type User struct {
	ID          string    `bson:"_id,omitempty" json:"id"`
	UserID      uint64    `bson:"user_id" json:"user_id"`  // 业务层用户ID（自增/分布式ID）
	Username    string    `bson:"username" json:"username"`
	Password    string    `bson:"password" json:"password"` // 注意：实际项目需存加密后的密码
	Nickname    string    `bson:"nickname" json:"nickname"`
	Email       string    `bson:"email,omitempty" json:"email"`
	Phone       string    `bson:"phone,omitempty" json:"phone"`
	Level       int32     `bson:"level" json:"level"`
	Experience  int64     `bson:"experience" json:"experience"`
	Gold        int64     `bson:"gold" json:"gold"`          // 金币（业务字段）
	Diamond     int64     `bson:"diamond" json:"diamond"`    // 钻石（业务字段）
	Avatar      string    `bson:"avatar,omitempty" json:"avatar"`
	Status      int32     `bson:"status" json:"status"`      // 0-正常 1-封禁
	LastLoginIP string    `bson:"last_login_ip" json:"last_login_ip"`
	LastLoginAt time.Time `bson:"last_login_at" json:"last_login_at"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`
}
