package server

import (
	"context"
	"crypto/md5"
	"fmt"
	"time"

	"github.com/puoxiu/cogame/internal/actor"
	"github.com/puoxiu/cogame/internal/cache"
	"github.com/puoxiu/cogame/internal/database/mongodb"
	"github.com/puoxiu/cogame/internal/logger"
	 "github.com/puoxiu/cogame/pkg/login_proto/proto"
)

// LoginServer 登录服务器
type LoginServer struct {
	*BaseServer
	userRepo  *mongodb.UserRepository
	userCache *cache.UserCache
}

// NewLoginServer 创建登录服务器
// todo 替换 RPC 服务注册为 gRPC 服务注册 ✅
func NewLoginServer(configFile, nodeID string) *LoginServer {
	baseServer, err := NewBaseServer(configFile, "login", nodeID)
	if err != nil {
		logger.Fatal(fmt.Sprintf("Failed to create base server: %v", err))
	}

	loginServer := &LoginServer{
		BaseServer: baseServer,
		userRepo:   mongodb.NewUserRepository(baseServer.mongoManager),
		userCache:  cache.NewUserCache(baseServer.redisManager),
	}

	// 注册通用服务
	if err := RegisterCommonServices(baseServer); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to register common services: %v", err))
	}

	// // 注册登录服务
	// loginService := NewLoginService(loginServer)
	// if err := baseServer.rpcServer.RegisterService(loginService); err != nil {
	// 	logger.Fatal(fmt.Sprintf("Failed to register login service: %v", err))
	// }

	// 移除原有 RPC 服务注册，改为初始化 gRPC 服务器
	loginGRPCService := NewLoginServiceImpl(loginServer)
	login_proto.RegisterLoginServiceServer(baseServer.grpcServer, loginGRPCService)
	logger.Debug("Login service registered to gRPC")

	// 创建登录Actor
	loginActor := NewLoginActor(loginServer)
	if err := baseServer.actorSystem.SpawnActor(loginActor); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to spawn login actor: %v", err))
	}

	return loginServer
}


// LoginServiceImpl 实现 gRPC 生成的 LoginServiceServer 接口
type LoginServiceImpl struct {
	login_proto.UnimplementedLoginServiceServer // 嵌入未实现的方法，避免编译错误
	server *LoginServer                   // 保留对 LoginServer 的依赖
}


// NewLoginServiceImpl 创建登录服务的 gRPC 实现
func NewLoginServiceImpl(server *LoginServer) *LoginServiceImpl {
	return &LoginServiceImpl{
		server: server,
	}
}


// Login 实现 gRPC 登录方法（复用原有业务逻辑）
func (ls *LoginServiceImpl) Login(ctx context.Context, req *login_proto.LoginRequest) (*login_proto.LoginResponse, error) {
	logger.Info(fmt.Sprintf("User login attempt: %s", req.Username))

	// 复用原有验证逻辑
	user, err := ls.server.userRepo.GetByUsername(req.Username)
	if err != nil {
		logger.Warn(fmt.Sprintf("User not found: %s", req.Username))
		return nil, fmt.Errorf("invalid username or password")
	}

	if !ls.verifyPassword(req.Password, user.Password) {
		logger.Warn(fmt.Sprintf("Password verification failed for user: %s", req.Username))
		return nil, fmt.Errorf("invalid username or password")
	}

	if user.Status != 0 {
		logger.Warn(fmt.Sprintf("User is banned: %s", req.Username))
		return nil, fmt.Errorf("user is banned")
	}

	token := ls.generateToken(user.UserID)

	// 更新用户登录信息（复用原有逻辑）
	err = ls.server.userRepo.UpdateFields(user.UserID, map[string]interface{}{
		"last_login_at": time.Now(),
		"last_login_ip": "0.0.0.0", // 实际应从请求获取
	})
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to update user login info: %v", err))
	}

	ls.server.userCache.SetUserInfo(user.UserID, user)
	sessionCache := cache.NewSessionCache(ls.server.redisManager)
	sessionCache.SetSession(token, user.UserID)

	logger.Info(fmt.Sprintf("User login successful: %s (ID: %d)", req.Username, user.UserID))

	return &login_proto.LoginResponse{
		UserId:   user.UserID,
		Token:    token,
		Nickname: user.Nickname,
		Level:    user.Level,
		Exp:      user.Experience,
		Gold:     user.Gold,
		Diamond:  user.Diamond,
	}, nil
}

// Logout 实现 gRPC 登出方法
func (ls *LoginServiceImpl) Logout(ctx context.Context, req *login_proto.LogoutRequest) (*login_proto.BaseResponse, error) {
	userID := req.Header.UserId
	if userID == 0 {
		return &login_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "invalid user id",
		}, nil
	}

	sessionID := req.Header.SessionId
	if sessionID != "" {
		sessionCache := cache.NewSessionCache(ls.server.redisManager)
		sessionCache.DeleteSession(sessionID)
	}

	ls.server.userCache.SetUserOffline(userID)
	logger.Info(fmt.Sprintf("User logout: %d", userID))

	return &login_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "logout success",
	}, nil
}



// ValidateToken 验证令牌
func (ls *LoginServiceImpl) ValidateToken(ctx context.Context, req *login_proto.BaseRequest) (*login_proto.BaseResponse, error) {
	sessionID := req.Header.SessionId
	if sessionID == "" {
		return &login_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "missing session id",
		}, nil
	}

	// 验证会话
	sessionCache := cache.NewSessionCache(ls.server.redisManager)
	userID, err := sessionCache.GetSession(sessionID)
	if err != nil {
		return &login_proto.BaseResponse{
			Header: req.Header,
			Code:   -2,
			Msg:    "invalid session",
		}, nil
	}

	// 刷新会话
	sessionCache.RefreshSession(sessionID)

	return &login_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "token valid",
		Data:   []byte(fmt.Sprintf(`{"user_id":%d}`, userID)),
	}, nil
}

// RefreshToken 刷新令牌
func (ls *LoginServiceImpl) RefreshToken(ctx context.Context, req *login_proto.BaseRequest) (*login_proto.BaseResponse, error) {
	userID := req.Header.UserId

	if userID == 0 {
		return &login_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "invalid user id",
		}, nil
	}

	// 生成新令牌
	newToken := ls.generateToken(userID)

	// 删除旧会话
	oldSessionID := req.Header.SessionId
	if oldSessionID != "" {
		sessionCache := cache.NewSessionCache(ls.server.redisManager)
		sessionCache.DeleteSession(oldSessionID)
	}

	// 创建新会话
	sessionCache := cache.NewSessionCache(ls.server.redisManager)
	sessionCache.SetSession(newToken, userID)

	return &login_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "token refreshed",
		Data:   []byte(fmt.Sprintf(`{"token":"%s"}`, newToken)),
	}, nil
}

// hashPassword 哈希密码
func (ls *LoginServiceImpl) hashPassword(password string) string {
	hash := md5.Sum([]byte(password + "co_game_salt"))
	return fmt.Sprintf("%x", hash)
}

// verifyPassword 验证密码
func (ls *LoginServiceImpl) verifyPassword(plainPassword, hashedPassword string) bool {
	return ls.hashPassword(plainPassword) == hashedPassword
}


// generateToken 生成令牌
func (ls *LoginServiceImpl) generateToken(userID uint64) string {
	data := fmt.Sprintf("%d_%d_%s", userID, time.Now().Unix(), "co_token_salt")
	hash := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", hash)
}

















// LoginActor 登录Actor
type LoginActor struct {
	*actor.BaseActor
	server *LoginServer
}

// NewLoginActor 创建登录Actor
func NewLoginActor(server *LoginServer) *LoginActor {
	baseActor := actor.NewBaseActor("login_actor", "login", 1000)

	return &LoginActor{
		BaseActor: baseActor,
		server:    server,
	}
}

// OnReceive 处理消息
func (la *LoginActor) OnReceive(ctx context.Context, msg actor.Message) error {
	switch msg.GetType() {
	case actor.MSG_TYPE_USER_LOGIN:
		return la.handleUserLogin(msg)
	case actor.MSG_TYPE_USER_LOGOUT:
		return la.handleUserLogout(msg)
	default:
		logger.Debug(fmt.Sprintf("Unknown message type: %s", msg.GetType()))
	}

	return nil
}

// OnStart 启动时处理
func (la *LoginActor) OnStart(ctx context.Context) error {
	logger.Info("Login actor started")
	return nil
}

// OnStop 停止时处理
func (la *LoginActor) OnStop(ctx context.Context) error {
	logger.Info("Login actor stopped")
	return nil
}

// handleUserLogin 处理用户登录
func (la *LoginActor) handleUserLogin(msg actor.Message) error {
	logger.Debug("Handling user login in login actor")
	// 可以在这里处理登录相关的异步逻辑
	// 比如记录登录日志、更新统计信息等
	return nil
}

// handleUserLogout 处理用户登出
func (la *LoginActor) handleUserLogout(msg actor.Message) error {
	logger.Debug("Handling user logout in login actor")
	// 处理登出相关的异步逻辑
	return nil
}
