package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Room 房间模型
type Room struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	RoomID         uint64             `bson:"room_id" json:"room_id"`
	RoomName       string             `bson:"room_name" json:"room_name"`
	GameType       int32              `bson:"game_type" json:"game_type"`
	MaxPlayers     int32              `bson:"max_players" json:"max_players"`
	CurrentPlayers int32              `bson:"current_players" json:"current_players"`
	Status         int32              `bson:"status" json:"status"` // 0-等待中 1-游戏中 2-已结束
	IsPrivate      bool               `bson:"is_private" json:"is_private"`
	Password       string             `bson:"password,omitempty" json:"password,omitempty"`
	OwnerID        uint64             `bson:"owner_id" json:"owner_id"`
	Players        []RoomPlayer       `bson:"players" json:"players"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at" json:"updated_at"`
}

// RoomPlayer 房间玩家信息
type RoomPlayer struct {
	UserID   uint64 `bson:"user_id" json:"user_id"`
	Nickname string `bson:"nickname" json:"nickname"`
	Level    int32  `bson:"level" json:"level"`
	Status   int32  `bson:"status" json:"status"` // 0-等待 1-准备 2-游戏中
	JoinTime int64  `bson:"join_time" json:"join_time"`
}