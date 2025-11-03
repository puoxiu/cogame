package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/puoxiu/cogame/internal/database/mongodb"
	"github.com/puoxiu/cogame/internal/logger"
	"github.com/puoxiu/cogame/internal/model"
	lobby_proto "github.com/puoxiu/cogame/pkg/lobby_proto/proto"
	"google.golang.org/protobuf/proto"
)

// LobbyServer 游戏大厅服务器
type LobbyServer struct {
	*BaseServer
	roomRepo   *mongodb.RoomRepository
	nextRoomID uint64
	idMutex    sync.Mutex
}


// NewLobbyServer 创建游戏大厅服务器
func NewLobbyServer(configFile, nodeID string) *LobbyServer {
	baseServer, err := NewBaseServer(configFile, "lobby", nodeID)
	if err != nil {
		logger.Fatal(fmt.Sprintf("Failed to create base server: %v", err))
	}

	lobbyServer := &LobbyServer{
		BaseServer: baseServer,
		roomRepo:   mongodb.NewRoomRepository(baseServer.mongoManager),
		nextRoomID: 1000, // 房间ID从1000开始
	}

	// 注册通用服务
	if err := RegisterCommonServices(baseServer); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to register common services: %v", err))
	}

	// 注册大厅服务
	// lobbyService := NewLobbyService(lobbyServer)
	// if err := baseServer.rpcServer.RegisterService(lobbyService); err != nil {
	// 	logger.Fatal(fmt.Sprintf("Failed to register lobby service: %v", err))
	// }
	lobbyGRPCService := NewLobbyServiceImpl(lobbyServer)
	lobby_proto.RegisterLobbyServiceServer(baseServer.grpcServer, lobbyGRPCService)
	logger.Debug("Lobby gRPC service registered")


	return lobbyServer
}

// generateRoomID 生成房间ID
func (ls *LobbyServer) generateRoomID() uint64 {
	ls.idMutex.Lock()
	defer ls.idMutex.Unlock()
	id := ls.nextRoomID
	ls.nextRoomID++
	return id
}

// ------------------------------------ lobby RPC服务 ------------------------------------//

// LobbyServiceImpl 实现gRPC生成的LobbyServiceServer接口
type LobbyServiceImpl struct {
	lobby_proto.UnimplementedLobbyServiceServer // 嵌入默认实现，兼容未实现方法
	server *LobbyServer                         // 持有大厅服务器实例
}

func NewLobbyServiceImpl(server *LobbyServer) *LobbyServiceImpl {
	return &LobbyServiceImpl{server: server}
}

// GetRoomList 获取房间列表
func (ls *LobbyServiceImpl) GetRoomList(ctx context.Context, req *lobby_proto.BaseRequest) (*lobby_proto.BaseResponse, error) {
	// 验证用户ID
	userID := req.Header.GetUserId()
	if userID == 0 {
		logger.Error("GetRoomList: invalid user id")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "invalid user id",
		}, nil
	}

	// 解析请求参数（可选）
	gameType := int32(0) // 默认获取所有类型
	limit := int64(20)   // 默认每页20个
	offset := int64(0)   // 默认从第一页开始

	// 如果有请求数据，尝试解析
	if len(req.Data) > 0 {
		// 这里可以解析分页参数，简化处理
	}

	// 获取房间列表
	rooms, err := ls.server.roomRepo.GetRoomList(gameType, limit, offset)
	if err != nil {
		logger.Error(fmt.Sprintf("GetRoomList: failed to get room list: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -2,
			Msg:    "failed to get room list",
		}, nil
	}

	// 获取用户信息用于填充房间详情
	userRepo := mongodb.NewUserRepository(ls.server.mongoManager)

	// 转换为proto格式
	var roomInfos []*lobby_proto.RoomInfo
	for _, room := range rooms {
		// 获取房主信息
		owner, err := userRepo.GetByUserID(room.OwnerID)
		if err != nil {
			logger.Warn(fmt.Sprintf("GetRoomList: failed to get owner info %d: %v", room.OwnerID, err))
			continue
		}

		ownerInfo := &lobby_proto.GamePlayerInfo{
			UserId:   owner.UserID,
			Nickname: owner.Nickname,
			Level:    owner.Level,
			Status:   0, // 房主状态
		}

		// 转换玩家列表
		var players []*lobby_proto.GamePlayerInfo
		for _, player := range room.Players {
			playerInfo := &lobby_proto.GamePlayerInfo{
				UserId:   player.UserID,
				Nickname: player.Nickname,
				Level:    player.Level,
				Status:   player.Status,
			}
			players = append(players, playerInfo)
		}

		roomInfo := &lobby_proto.RoomInfo{
			RoomId:         room.RoomID,
			RoomName:       room.RoomName,
			GameType:       room.GameType,
			CurrentPlayers: room.CurrentPlayers,
			MaxPlayers:     room.MaxPlayers,
			Status:         room.Status,
			IsPrivate:      room.IsPrivate,
			Owner:          ownerInfo,
			Players:        players,
			CreatedTime:    uint32(room.CreatedAt.Unix()),
		}

		roomInfos = append(roomInfos, roomInfo)
	}

	// 获取总数
	total, err := ls.server.roomRepo.CountRooms(gameType)
	if err != nil {
		logger.Error(fmt.Sprintf("GetRoomList: failed to count rooms: %v", err))
		total = int64(len(roomInfos)) // 使用当前数量作为备选
	}

	// 构造响应
	roomListResp := &lobby_proto.RoomListResponse{
		Rooms: roomInfos,
		Total: int32(total),
	}

	responseData, err := proto.Marshal(roomListResp)
	if err != nil {
		logger.Error(fmt.Sprintf("GetRoomList: failed to marshal response: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -3,
			Msg:    "failed to create response",
		}, nil
	}

	logger.Info(fmt.Sprintf("User %d retrieved room list with %d rooms", userID, len(roomInfos)))

	return &lobby_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "success",
		Data:   responseData,
	}, nil
}



// CreateRoom 创建房间
func (ls *LobbyServiceImpl) CreateRoom(ctx context.Context, req *lobby_proto.BaseRequest) (*lobby_proto.BaseResponse, error) {
	// 验证用户ID
	userID := req.Header.GetUserId()
	if userID == 0 {
		logger.Error("CreateRoom: invalid user id")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "invalid user id",
		}, nil
	}

	// 解析请求数据
	var createRoomReq lobby_proto.CreateRoomRequest
	if err := proto.Unmarshal(req.Data, &createRoomReq); err != nil {
		logger.Error(fmt.Sprintf("CreateRoom: failed to unmarshal request: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -2,
			Msg:    "invalid request data",
		}, nil
	}

	roomName := createRoomReq.GetRoomName()
	gameType := createRoomReq.GetGameType()
	maxPlayers := createRoomReq.GetMaxPlayers()
	isPrivate := createRoomReq.GetIsPrivate()
	password := createRoomReq.GetPassword()

	// 验证房间参数
	if roomName == "" {
		logger.Error("CreateRoom: room name is empty")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -3,
			Msg:    "room name cannot be empty",
		}, nil
	}

	if maxPlayers < 2 || maxPlayers > 8 {
		logger.Error(fmt.Sprintf("CreateRoom: invalid max players %d", maxPlayers))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -4,
			Msg:    "max players must be between 2 and 8",
		}, nil
	}

	if isPrivate && password == "" {
		logger.Error("CreateRoom: private room requires password")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -5,
			Msg:    "private room requires password",
		}, nil
	}

	// 获取用户信息
	userRepo := mongodb.NewUserRepository(ls.server.mongoManager)
	user, err := userRepo.GetByUserID(userID)
	if err != nil {
		logger.Error(fmt.Sprintf("CreateRoom: failed to get user %d: %v", userID, err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -6,
			Msg:    "user not found",
		}, nil
	}

	// 生成房间ID
	roomID := ls.server.generateRoomID()

	// 创建房间对象
	room := &model.Room{
		RoomID:         roomID,
		RoomName:       roomName,
		GameType:       gameType,
		MaxPlayers:     maxPlayers,
		CurrentPlayers: 1, // 房主算一个玩家
		Status:         0, // 等待中
		IsPrivate:      isPrivate,
		Password:       password,
		OwnerID:        userID,
		Players: []model.RoomPlayer{
			{
				UserID:   userID,
				Nickname: user.Nickname,
				Level:    user.Level,
				Status:   1, // 房主默认准备状态
				JoinTime: time.Now().Unix(),
			},
		},
	}

	// 保存到数据库
	if err = ls.server.roomRepo.CreateRoom(room); err != nil {
		logger.Error(fmt.Sprintf("CreateRoom: failed to create room: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -7,
			Msg:    "failed to create room",
		}, nil
	}

	logger.Info(fmt.Sprintf("User %s (ID: %d) created room %d: %s", user.Nickname, userID, roomID, roomName))

	// 构造响应数据
	ownerInfo := &lobby_proto.GamePlayerInfo{
		UserId:   user.UserID,
		Nickname: user.Nickname,
		Level:    user.Level,
		Status:   1,
	}

	roomInfo := &lobby_proto.RoomInfo{
		RoomId:         roomID,
		RoomName:       roomName,
		GameType:       gameType,
		CurrentPlayers: 1,
		MaxPlayers:     maxPlayers,
		Status:         0,
		IsPrivate:      isPrivate,
		Owner:          ownerInfo,
		Players:        []*lobby_proto.GamePlayerInfo{ownerInfo},
		CreatedTime:    uint32(room.CreatedAt.Unix()),
	}

	responseData, err := proto.Marshal(roomInfo)
	if err != nil {
		logger.Error(fmt.Sprintf("CreateRoom: failed to marshal response: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -8,
			Msg:    "failed to create response",
		}, nil
	}

	return &lobby_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "room created successfully",
		Data:   responseData,
	}, nil
}

// JoinRoom 加入房间
func (ls *LobbyServiceImpl) JoinRoom(ctx context.Context, req *lobby_proto.BaseRequest) (*lobby_proto.BaseResponse, error) {
	// 验证用户ID
	userID := req.Header.GetUserId()
	if userID == 0 {
		logger.Error("JoinRoom: invalid user id")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "invalid user id",
		}, nil
	}

	// 解析请求数据
	var joinRoomReq lobby_proto.JoinRoomRequest
	if err := proto.Unmarshal(req.Data, &joinRoomReq); err != nil {
		logger.Error(fmt.Sprintf("JoinRoom: failed to unmarshal request: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -2,
			Msg:    "invalid request data",
		}, nil
	}

	roomID := joinRoomReq.GetRoomId()
	password := joinRoomReq.GetPassword()

	// 验证房间ID
	if roomID == 0 {
		logger.Error("JoinRoom: invalid room id")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -3,
			Msg:    "invalid room id",
		}, nil
	}

	// 获取房间信息
	room, err := ls.server.roomRepo.GetRoomByID(roomID)
	if err != nil {
		logger.Error(fmt.Sprintf("JoinRoom: room %d not found: %v", roomID, err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -4,
			Msg:    "room not found",
		}, nil
	}

	// 检查房间状态
	if room.Status != 0 {
		logger.Error(fmt.Sprintf("JoinRoom: room %d is not waiting (status: %d)", roomID, room.Status))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -5,
			Msg:    "room is not available",
		}, nil
	}

	// 检查房间是否已满
	if room.CurrentPlayers >= room.MaxPlayers {
		logger.Error(fmt.Sprintf("JoinRoom: room %d is full (%d/%d)", roomID, room.CurrentPlayers, room.MaxPlayers))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -6,
			Msg:    "room is full",
		}, nil
	}

	// 检查用户是否已在房间中
	for _, player := range room.Players {
		if player.UserID == userID {
			logger.Error(fmt.Sprintf("JoinRoom: user %d already in room %d", userID, roomID))
			return &lobby_proto.BaseResponse{
				Header: req.Header,
				Code:   -7,
				Msg:    "already in room",
			}, nil
		}
	}

	// 检查私有房间密码
	if room.IsPrivate && room.Password != password {
		logger.Error(fmt.Sprintf("JoinRoom: wrong password for private room %d", roomID))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -8,
			Msg:    "wrong password",
		}, nil
	}

	// 获取用户信息
	userRepo := mongodb.NewUserRepository(ls.server.mongoManager)
	user, err := userRepo.GetByUserID(userID)
	if err != nil {
		logger.Error(fmt.Sprintf("JoinRoom: failed to get user %d: %v", userID, err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -9,
			Msg:    "user not found",
		}, nil
	}

	// 创建玩家对象
	player := model.RoomPlayer{
		UserID:   userID,
		Nickname: user.Nickname,
		Level:    user.Level,
		Status:   0, // 等待状态
		JoinTime: time.Now().Unix(),
	}

	// 添加玩家到房间
	if err = ls.server.roomRepo.AddPlayerToRoom(roomID, player); err != nil {
		logger.Error(fmt.Sprintf("JoinRoom: failed to add player to room: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -10,
			Msg:    "failed to join room",
		}, nil
	}

	logger.Info(fmt.Sprintf("User %s (ID: %d) joined room %d: %s", user.Nickname, userID, roomID, room.RoomName))

	// 重新获取房间信息（包含更新后的玩家列表）
	updatedRoom, err := ls.server.roomRepo.GetRoomByID(roomID)
	if err != nil {
		logger.Error(fmt.Sprintf("JoinRoom: failed to get updated room info: %v", err))
		// 不返回错误，使用原房间信息
		updatedRoom = room
	}

	// 构造响应数据
	var players []*lobby_proto.GamePlayerInfo
	for _, p := range updatedRoom.Players {
		playerInfo := &lobby_proto.GamePlayerInfo{
			UserId:   p.UserID,
			Nickname: p.Nickname,
			Level:    p.Level,
			Status:   p.Status,
		}
		players = append(players, playerInfo)
	}

	// 获取房主信息
	userRepo2 := mongodb.NewUserRepository(ls.server.mongoManager)
	owner, err := userRepo2.GetByUserID(updatedRoom.OwnerID)
	if err != nil {
		logger.Warn(fmt.Sprintf("JoinRoom: failed to get owner info: %v", err))
	}

	var ownerInfo *lobby_proto.GamePlayerInfo
	if owner != nil {
		ownerInfo = &lobby_proto.GamePlayerInfo{
			UserId:   owner.UserID,
			Nickname: owner.Nickname,
			Level:    owner.Level,
			Status:   1, // 房主状态
		}
	}

	roomInfo := &lobby_proto.RoomInfo{
		RoomId:         updatedRoom.RoomID,
		RoomName:       updatedRoom.RoomName,
		GameType:       updatedRoom.GameType,
		CurrentPlayers: updatedRoom.CurrentPlayers,
		MaxPlayers:     updatedRoom.MaxPlayers,
		Status:         updatedRoom.Status,
		IsPrivate:      updatedRoom.IsPrivate,
		Owner:          ownerInfo,
		Players:        players,
		CreatedTime:    uint32(updatedRoom.CreatedAt.Unix()),
	}

	responseData, err := proto.Marshal(roomInfo)
	if err != nil {
		logger.Error(fmt.Sprintf("JoinRoom: failed to marshal response: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -11,
			Msg:    "failed to create response",
		}, nil
	}

	return &lobby_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "joined room successfully",
		Data:   responseData,
	}, nil
}

// LeaveRoom 离开房间
func (ls *LobbyServiceImpl) LeaveRoom(ctx context.Context, req *lobby_proto.BaseRequest) (*lobby_proto.BaseResponse, error) {
	// 验证用户ID
	userID := req.Header.GetUserId()
	if userID == 0 {
		logger.Error("LeaveRoom: invalid user id")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "invalid user id",
		}, nil
	}

	// 解析请求数据
	var leaveRoomReq lobby_proto.JoinRoomRequest // 复用JoinRoomRequest结构，只需要RoomId
	if err := proto.Unmarshal(req.Data, &leaveRoomReq); err != nil {
		logger.Error(fmt.Sprintf("LeaveRoom: failed to unmarshal request: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -2,
			Msg:    "invalid request data",
		}, nil
	}

	roomID := leaveRoomReq.GetRoomId()

	// 验证房间ID
	if roomID == 0 {
		logger.Error("LeaveRoom: invalid room id")
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -3,
			Msg:    "invalid room id",
		}, nil
	}

	// 获取房间信息
	room, err := ls.server.roomRepo.GetRoomByID(roomID)
	if err != nil {
		logger.Error(fmt.Sprintf("LeaveRoom: room %d not found: %v", roomID, err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -4,
			Msg:    "room not found",
		}, nil
	}

	// 检查用户是否在房间中
	userInRoom := false
	for _, player := range room.Players {
		if player.UserID == userID {
			userInRoom = true
			break
		}
	}

	if !userInRoom {
		logger.Error(fmt.Sprintf("LeaveRoom: user %d not in room %d", userID, roomID))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -5,
			Msg:    "not in room",
		}, nil
	}

	// 获取用户信息用于日志
	userRepo := mongodb.NewUserRepository(ls.server.mongoManager)
	user, err := userRepo.GetByUserID(userID)
	if err != nil {
		logger.Warn(fmt.Sprintf("LeaveRoom: failed to get user info: %v", err))
	}

	// 如果是房主离开，需要特殊处理
	if room.OwnerID == userID {
		// 如果房间只有房主一个人，删除房间
		if room.CurrentPlayers <= 1 {
			if err = ls.server.roomRepo.DeleteRoom(roomID); err != nil {
				logger.Error(fmt.Sprintf("LeaveRoom: failed to delete room: %v", err))
				return &lobby_proto.BaseResponse{
					Header: req.Header,
					Code:   -6,
					Msg:    "failed to leave room",
				}, nil
			}
			logger.Info(fmt.Sprintf("Room %d deleted as owner left", roomID))
		} else {
			// 转移房主权限给第一个其他玩家
			var newOwnerID uint64
			for _, player := range room.Players {
				if player.UserID != userID {
					newOwnerID = player.UserID
					break
				}
			}

			if newOwnerID != 0 {
				// 先移除当前玩家
				if err = ls.server.roomRepo.RemovePlayerFromRoom(roomID, userID); err != nil {
					logger.Error(fmt.Sprintf("LeaveRoom: failed to remove player: %v", err))
					return &lobby_proto.BaseResponse{
						Header: req.Header,
						Code:   -7,
						Msg:    "failed to leave room",
					}, nil
				}

				// 更新房主
				room.OwnerID = newOwnerID
				if err = ls.server.roomRepo.UpdateRoom(room); err != nil {
					logger.Error(fmt.Sprintf("LeaveRoom: failed to update room owner: %v", err))
				}

				logger.Info(fmt.Sprintf("Room %d ownership transferred to user %d", roomID, newOwnerID))
			}
		}
	} else {
		// 普通玩家离开，直接移除
		if err = ls.server.roomRepo.RemovePlayerFromRoom(roomID, userID); err != nil {
			logger.Error(fmt.Sprintf("LeaveRoom: failed to remove player: %v", err))
			return &lobby_proto.BaseResponse{
				Header: req.Header,
				Code:   -8,
				Msg:    "failed to leave room",
			}, nil
		}
	}

	if user != nil {
		logger.Info(fmt.Sprintf("User %s (ID: %d) left room %d: %s", user.Nickname, userID, roomID, room.RoomName))
	} else {
		logger.Info(fmt.Sprintf("User %d left room %d: %s", userID, roomID, room.RoomName))
	}

	// 构造响应数据
	responseData := map[string]interface{}{
		"room_id": roomID,
		"left_at": time.Now().Unix(),
	}

	responseBytes, err := proto.Marshal(&lobby_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "left room successfully",
	})
	if err != nil {
		logger.Error(fmt.Sprintf("LeaveRoom: failed to marshal response: %v", err))
		return &lobby_proto.BaseResponse{
			Header: req.Header,
			Code:   -9,
			Msg:    "failed to create response",
		}, nil
	}

	// 简化处理，直接返回成功响应
	_ = responseData
	_ = responseBytes

	return &lobby_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "left room successfully",
	}, nil
}


