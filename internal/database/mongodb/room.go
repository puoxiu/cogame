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

// RoomRepository 房间数据仓库
type RoomRepository struct {
	collection *mongo.Collection
}


// NewRoomRepository 创建房间仓库
func NewRoomRepository(mm *MongoManager) *RoomRepository {
	collection := mm.GetCollection("rooms")

	// 创建索引
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "room_id", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "status", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "game_type", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "owner_id", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "created_at", Value: -1}},
		},
	}

	collection.Indexes().CreateMany(context.Background(), indexes)

	return &RoomRepository{
		collection: collection,
	}
}

// CreateRoom 创建房间
func (rr *RoomRepository) CreateRoom(room *model.Room) error {
	room.CreatedAt = time.Now()
	room.UpdatedAt = time.Now()

	result, err := rr.collection.InsertOne(context.Background(), room)
	if err != nil {
		return fmt.Errorf("failed to create room: %v", err)
	}

	room.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

// GetRoomByID 根据房间ID获取房间
func (rr *RoomRepository) GetRoomByID(roomID uint64) (*model.Room, error) {
	var room model.Room
	err := rr.collection.FindOne(context.Background(), bson.M{"room_id": roomID}).Decode(&room)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("room not found")
		}
		return nil, fmt.Errorf("failed to get room: %v", err)
	}
	return &room, nil
}

// GetRoomList 获取房间列表
func (rr *RoomRepository) GetRoomList(gameType int32, limit int64, offset int64) ([]*model.Room, error) {
	filter := bson.M{}
	if gameType > 0 {
		filter["game_type"] = gameType
	}
	// 只显示等待中的房间
	filter["status"] = 0

	options := options.Find().
		SetLimit(limit).
		SetSkip(offset).
		SetSort(bson.D{{Key: "created_at", Value: -1}})

	cursor, err := rr.collection.Find(context.Background(), filter, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get room list: %v", err)
	}
	defer cursor.Close(context.Background())

	var rooms []*model.Room
	if err := cursor.All(context.Background(), &rooms); err != nil {
		return nil, fmt.Errorf("failed to decode rooms: %v", err)
	}

	return rooms, nil
}


// UpdateRoom 更新房间信息
func (rr *RoomRepository) UpdateRoom(room *model.Room) error {
	room.UpdatedAt = time.Now()

	filter := bson.M{"room_id": room.RoomID}
	update := bson.M{"$set": room}

	_, err := rr.collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update room: %v", err)
	}
	return nil
}

// AddPlayerToRoom 添加玩家到房间
func (rr *RoomRepository) AddPlayerToRoom(roomID uint64, player model.RoomPlayer) error {
	filter := bson.M{"room_id": roomID}
	update := bson.M{
		"$push": bson.M{"players": player},
		"$inc":  bson.M{"current_players": 1},
		"$set":  bson.M{"updated_at": time.Now()},
	}

	_, err := rr.collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to add player to room: %v", err)
	}
	return nil
}

// RemovePlayerFromRoom 从房间移除玩家
func (rr *RoomRepository) RemovePlayerFromRoom(roomID uint64, userID uint64) error {
	filter := bson.M{"room_id": roomID}
	update := bson.M{
		"$pull": bson.M{"players": bson.M{"user_id": userID}},
		"$inc":  bson.M{"current_players": -1},
		"$set":  bson.M{"updated_at": time.Now()},
	}

	_, err := rr.collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to remove player from room: %v", err)
	}
	return nil
}

// DeleteRoom 删除房间
func (rr *RoomRepository) DeleteRoom(roomID uint64) error {
	filter := bson.M{"room_id": roomID}
	_, err := rr.collection.DeleteOne(context.Background(), filter)
	if err != nil {
		return fmt.Errorf("failed to delete room: %v", err)
	}
	return nil
}

// CountRooms 统计房间数量
func (rr *RoomRepository) CountRooms(gameType int32) (int64, error) {
	filter := bson.M{}
	if gameType > 0 {
		filter["game_type"] = gameType
	}
	filter["status"] = 0 // 只统计等待中的房间

	count, err := rr.collection.CountDocuments(context.Background(), filter)
	if err != nil {
		return 0, fmt.Errorf("failed to count rooms: %v", err)
	}
	return count, nil
}
