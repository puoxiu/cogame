package mongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/puoxiu/cogame/internal/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/bson/primitive"

)

// UserRepository 用户数据仓库
type UserRepository struct {
	collection *mongo.Collection
}

// NewUserRepository 创建用户仓库
func NewUserRepository(mm *MongoManager) *UserRepository {
	collection := mm.GetCollection("users")

	// 创建索引
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "user_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "username", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "email", Value: 1}},
		},
	}

	collection.Indexes().CreateMany(context.Background(), indexes)

	return &UserRepository{
		collection: collection,
	}
}


// Create 创建用户
func (ur *UserRepository) Create(user *model.User) error {
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()

	result, err := ur.collection.InsertOne(context.Background(), user)
	if err != nil {
		return fmt.Errorf("failed to create user: %v", err)
	}

	user.ID = result.InsertedID.(primitive.ObjectID).Hex()
	return nil
}

// GetByUserID 根据用户ID获取用户
func (ur *UserRepository) GetByUserID(userID uint64) (*model.User, error) {
	var user model.User
	err := ur.collection.FindOne(context.Background(), bson.M{"user_id": userID}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %v", err)
	}
	return &user, nil
}

// GetByUsername 根据用户名获取用户
func (ur *UserRepository) GetByUsername(username string) (*model.User, error) {
	var user model.User
	err := ur.collection.FindOne(context.Background(), bson.M{"username": username}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %v", err)
	}
	return &user, nil
}

// Update 更新用户
func (ur *UserRepository) Update(user *model.User) error {
	user.UpdatedAt = time.Now()

	filter := bson.M{"user_id": user.UserID}
	update := bson.M{"$set": user}

	_, err := ur.collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update user: %v", err)
	}
	return nil
}

// UpdateFields 更新指定字段
func (ur *UserRepository) UpdateFields(userID uint64, fields bson.M) error {
	fields["updated_at"] = time.Now()

	filter := bson.M{"user_id": userID}
	update := bson.M{"$set": fields}

	_, err := ur.collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update user fields: %v", err)
	}
	return nil
}

// Delete 删除用户
func (ur *UserRepository) Delete(userID uint64) error {
	filter := bson.M{"user_id": userID}
	_, err := ur.collection.DeleteOne(context.Background(), filter)
	if err != nil {
		return fmt.Errorf("failed to delete user: %v", err)
	}
	return nil
}

// List 获取用户列表
func (ur *UserRepository) List(offset, limit int64) ([]*model.User, error) {
	options := options.Find().
		SetSkip(offset).
		SetLimit(limit).
		SetSort(bson.D{{Key: "created_at", Value: -1}})

	cursor, err := ur.collection.Find(context.Background(), bson.M{}, options)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %v", err)
	}
	defer cursor.Close(context.Background())

	var users []*model.User
	if err := cursor.All(context.Background(), &users); err != nil {
		return nil, fmt.Errorf("failed to decode users: %v", err)
	}

	return users, nil
}
