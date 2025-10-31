package database

import (
	"github.com/puoxiu/cogame/internal/model"
	"go.mongodb.org/mongo-driver/bson"
)

// 注意：这里只定义 User 相关的抽象，其他 Repository（Friend/Mail）的接口也放这个文件，按模块分组

// ------------------------------
// User 相关抽象接口
// ------------------------------

// IUserRepository User 数据访问层抽象接口
type IUserRepository interface {
	Create(user *model.User) error
	GetByUserID(userID uint64) (*model.User, error)
	GetByUsername(username string) (*model.User, error)
	Update(user *model.User) error
	UpdateFields(userID uint64, fields bson.M) error	// 部分更新用户字段（如只更新金币、等级）
	Delete(userID uint64) error
	List(offset, limit int64) ([]*model.User, error)
}
