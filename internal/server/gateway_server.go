package server

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"github.com/puoxiu/cogame/internal/actor"
	"github.com/puoxiu/cogame/internal/cache"
	"github.com/puoxiu/cogame/internal/logger"
	"github.com/puoxiu/cogame/internal/network"
	gateway_proto "github.com/puoxiu/cogame/pkg/gateway_proto/proto"
	login_proto "github.com/puoxiu/cogame/pkg/login_proto/proto"
)



type GatewayServer struct {
	*BaseServer 
	messageHandler *GatewayMessageHandler // 客户端消息处理器
}


func NewGatewayServer(configFile, nodeID string) *GatewayServer {
	// 1. 创建BaseServer（复用通用组件：Redis、ETCD、RPC Server等）
	baseServer, err := NewBaseServer(configFile, "gateway", nodeID)
	if err != nil {
		logger.Fatal(fmt.Sprintf("Failed to create base server: %v", err))
	}

	// 2. 初始化网关消息处理器
	gatewayServer := &GatewayServer{
		BaseServer:     baseServer,
		messageHandler: &GatewayMessageHandler{server: baseServer},
	}
	
	// 3. 初始化TCP服务器， 所有客户端连接都由网关处理
	tcpServer := network.NewTCPServer(
		"0.0.0.0",                          // 监听所有网卡
		baseServer.config.Network.TCPPort,  // 从配置读TCP端口（如8080）
		gatewayServer.messageHandler,       // 绑定消息处理器（接收到消息后交给它处理）
		baseServer.config.Network.MaxConnections, // 最大连接数（防止过载）
	)
	gatewayServer.BaseServer.tcpServer = tcpServer

	// 4. 注册通用服务
	if err := RegisterCommonServices(baseServer); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to register common services: %v", err))
	}

	// 5. 注册网关gRPC服务
	gatewayGRPCService := NewGatewayServiceImpl(gatewayServer)
	gateway_proto.RegisterGatewayServiceServer(baseServer.grpcServer, gatewayGRPCService)
	logger.Debug("Gateway gRPC service registered")

	// 6. 创建网关Actror
	gatewayActor := NewGatewayActor(gatewayServer)
	if err := baseServer.actorSystem.SpawnActor(gatewayActor); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to spawn gateway actor: %v", err))
	}

	return gatewayServer
}



// Start 启动网关服务器
func (gs *GatewayServer) Start() error {
	// 启动基础服务器
	if err := gs.BaseServer.Start(); err != nil {
		return err
	}

	// 启动TCP服务器
	if err := gs.tcpServer.Start(); err != nil {
		return fmt.Errorf("failed to start tcp server: %v", err)
	}

	logger.Info(fmt.Sprintf("Gateway server %s started on TCP port %d",gs.nodeID, gs.config.Network.TCPPort))

	return nil
}

// Stop 停止网关服务器
func (gs *GatewayServer) Stop() error {
	// BaseServer中已经实现了tcpServer的Stop，这里不需要重复实现
	// if gs.tcpServer != nil {
	// 	gs.tcpServer.Stop()
	// }

	return gs.BaseServer.Stop()
}


// ------------------------------------ 网关消息处理器 ------------------------------------//
// 每个需要TCP连接的服务器，都需要实现消息处理器接口

// GatewayMessageHandler 网关消息处理器
type GatewayMessageHandler struct {
	server *BaseServer
}

// NewGatewayMessageHandler 创建网关消息处理器
func NewGatewayMessageHandler(server *BaseServer) *GatewayMessageHandler {
	return &GatewayMessageHandler{
		server: server,
	}
}


// HandleMessage 处理消息
func (gmh *GatewayMessageHandler) HandleMessage(conn *network.Connection, data []byte) error {
	// 解析消息头
	if len(data) < 4 {
		return fmt.Errorf("message too short")
	}

	// 解析消息ID
	msgID := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	logger.Debug(fmt.Sprintf("收到 message ID: %d from connection %d", msgID, conn.ID))

	// 解析Protobuf消息
	var request gateway_proto.BaseRequest
	if err := proto.Unmarshal(data[4:], &request); err != nil {
		return fmt.Errorf("failed to unmarshal request: %v", err)
	}

	logger.Debug(fmt.Sprintf("解析 BaseRequest: %v, 正常头部字段是字符串, data 是 []byte", request))

	// 路由消息到对应的处理器
	return gmh.routeMessage(conn, msgID, &request)
}

// routeMessage 路由消息
// 对于登录（1001）、心跳（1002）、登出（1003）消息，网关会直接处理；其他消息（ 2000~7000 区间的消息）会由网关转发到对应的服务
func (gmh *GatewayMessageHandler) routeMessage(conn *network.Connection, msgID uint32, request *gateway_proto.BaseRequest) error {
	switch msgID {
	case 1001: // 用户登录
		return gmh.handleLogin(conn, request)
	case 1002: // 心跳
		return gmh.handleHeartbeat(conn, request)
	case 1003: // 用户登出
		return gmh.handleLogout(conn, request)
	default:
		// 转发到其他服务器
		return gmh.forwardMessage(conn, msgID, request)
	}
}

// handleLogin 处理登录
func (gmh *GatewayMessageHandler) handleLogin(conn *network.Connection, request *gateway_proto.BaseRequest) error {
	// 解析登录请求
	// var loginReq login_proto.LoginRequest
	logger.Debug(fmt.Sprintf("收到登录请求: %v, 现在需要解析data", request))
	var loginReq login_proto.LoginRequest
	if err := proto.Unmarshal(request.Data, &loginReq); err != nil {
		return fmt.Errorf("failed to unmarshal login request: %v", err)
	}
	logger.Debug(fmt.Sprintf("解析 LoginRequest, username = %s, password = %s, platform = %s, version = %s", loginReq.Username, loginReq.Password, loginReq.Platform, loginReq.Version))

	// 获取登录服务
	loginService := gmh.server.discovery.GetService("login")
	if loginService == nil {
		logger.Debug("登录服务 它不存在")
		return gmh.sendError(conn, request, -1, "login service not available")
	}

	// TODO: 通过RPC调用登录服务
	// 简化实现：直接返回成功响应
	logger.Debug(fmt.Sprintf("登录服务存在,登录请求成功, username = %s, password = %s, platform = %s, version = %s", loginReq.Username, loginReq.Password, loginReq.Platform, loginReq.Version))

	// 模拟登录成功响应
	loginResp := login_proto.LoginResponse{
		UserId: 12345,
		Token:  "mock_token_" + loginReq.Username,
	}

	// 绑定连接到用户
	conn.UserID = loginResp.UserId

	// 设置用户在线状态
	userCache := cache.NewUserCache(gmh.server.redisManager)
	userCache.SetUserOnline(loginResp.UserId, gmh.server.nodeID)

	// 发送响应
	return gmh.sendResponse(conn, request, 0, "login success", &loginResp)
}


// handleHeartbeat 处理心跳
func (gmh *GatewayMessageHandler) handleHeartbeat(conn *network.Connection, request *gateway_proto.BaseRequest) error {
	// 更新连接活动时间
	conn.LastActivity = time.Now()

	// 发送心跳响应
	return gmh.sendResponse(conn, request, 0, "pong", nil)
}

// handleLogout 处理登出
func (gmh *GatewayMessageHandler) handleLogout(conn *network.Connection, request *gateway_proto.BaseRequest) error {
	if conn.UserID != 0 {
		// 设置用户离线
		userCache := cache.NewUserCache(gmh.server.redisManager)
		userCache.SetUserOffline(conn.UserID)

		logger.Info(fmt.Sprintf("User %d logged out from connection %d", conn.UserID, conn.ID))
	}

	// 关闭连接
	conn.Close()

	return nil
}


// forwardMessage 转发消息
func (gmh *GatewayMessageHandler) forwardMessage(conn *network.Connection, msgID uint32, request *gateway_proto.BaseRequest) error {
	// 根据消息ID确定目标服务
	var targetService string

	switch {
	case msgID >= 2000 && msgID < 3000:
		targetService = "lobby"
	case msgID >= 3000 && msgID < 4000:
		targetService = "game"
	case msgID >= 4000 && msgID < 5000:
		targetService = "friend"
	case msgID >= 5000 && msgID < 6000:
		targetService = "chat"
	case msgID >= 6000 && msgID < 7000:
		targetService = "mail"
	default:
		return gmh.sendError(conn, request, -1, "unknown message type")
	}

	// 获取目标服务实例
	service := gmh.server.discovery.GetService(targetService)
	if service == nil {
		return gmh.sendError(conn, request, -2, fmt.Sprintf("%s service not available", targetService))
	}

	// TODO: 通过RPC转发消息
	// 简化实现：直接返回成功响应
	logger.Info(fmt.Sprintf("Forwarding message ID %d to service: %s", msgID, targetService))

	// 模拟服务调用成功响应
	return gmh.sendResponse(conn, request, 0, "success", nil)
}



// sendError 发送错误响应
func (gmh *GatewayMessageHandler) sendError(conn *network.Connection, request *gateway_proto.BaseRequest, code int32, msg string) error {
	return gmh.sendResponse(conn, request, code, msg, nil)
}


// sendResponse 发送响应
func (gmh *GatewayMessageHandler) sendResponse(conn *network.Connection, request *gateway_proto.BaseRequest, code int32, msg string, data proto.Message) error {
	response := &gateway_proto.BaseResponse{
		Header: request.Header,
		Code:   code,
		Msg:    msg,
	}

	if data != nil {
		responseData, err := proto.Marshal(data)
		if err != nil {
			return fmt.Errorf("failed to marshal response data: %v", err)
		}
		response.Data = responseData
	}

	responseBytes, err := proto.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %v", err)
	}

	// 添加消息长度头
	length := len(responseBytes)
	message := make([]byte, 4+length)
	message[0] = byte(length >> 24)
	message[1] = byte(length >> 16)
	message[2] = byte(length >> 8)
	message[3] = byte(length)
	copy(message[4:], responseBytes)

	return conn.Write(message)
}


// ------------------------------------ 网关RPC服务 ------------------------------------//

// GatewayServiceImpl 实现gRPC生成的GatewayServiceServer接口
type GatewayServiceImpl struct {
	gateway_proto.UnimplementedGatewayServiceServer // 嵌入默认实现，兼容未实现方法
	server *GatewayServer                           // 持有网关服务器实例
}

// NewGatewayServiceImpl 创建网关服务实现
func NewGatewayServiceImpl(server *GatewayServer) *GatewayServiceImpl {
	return &GatewayServiceImpl{
		server: server,
	}
}

// GetConnectionCount 获取当前连接数
func (gs *GatewayServiceImpl) GetConnectionCount(ctx context.Context, req *gateway_proto.BaseRequest) (*gateway_proto.BaseResponse, error) {
	count := gs.server.tcpServer.GetConnectionCount()
	return &gateway_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    fmt.Sprintf("connection count: %d", count),
	}, nil
}


// SendToUser 发送消息给指定用户--todo
func (gs *GatewayServiceImpl) SendToUser(ctx context.Context, req *gateway_proto.BaseRequest) (*gateway_proto.BaseResponse, error) {
	// 这里需要从请求中解析目标用户ID和消息内容
	// 简化实现，实际需要定义具体的消息格式

	response := &gateway_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "message sent successfully--todo",
	}

	return response, nil
}

// BroadcastMessage 广播消息--todo消息协议
func (gs *GatewayServiceImpl) BroadcastMessage(ctx context.Context, req *gateway_proto.BaseRequest) (*gateway_proto.BaseResponse, error) {
	// 广播消息给所有连接的用户
	gs.server.tcpServer.Broadcast(req.Data)

	response := &gateway_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "message broadcasted",
	}

	return response, nil
}

// KickUser 踢出用户--todo
func (gs *GatewayServiceImpl) KickUser(ctx context.Context, req *gateway_proto.BaseRequest) (*gateway_proto.BaseResponse, error) {
	// 这里需要从请求中解析用户ID, 并从连接列表中移除-todo
	// 简化实现

	response := &gateway_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "user kicked--todo",
	}

	return response, nil
}



// ------------------------------------ 网关Actor ------------------------------------//

type GatewayActor struct {
	*actor.BaseActor
	server *GatewayServer
}

// NewGatewayActor 创建网关Actor
func NewGatewayActor(server *GatewayServer) *GatewayActor {
	baseActor := actor.NewBaseActor("gateway_actor", "gateway", 1000)

	return &GatewayActor{
		BaseActor: baseActor,
		server:    server,
	}
}

// OnReceive 处理消息
func (ga *GatewayActor) OnReceive(ctx context.Context, msg actor.Message) error {
	switch msg.GetType() {
	case actor.MSG_TYPE_USER_LOGIN:
		return ga.handleUserLogin(msg)
	case actor.MSG_TYPE_USER_LOGOUT:
		return ga.handleUserLogout(msg)
	default:
		logger.Debug(fmt.Sprintf("Unknown message type: %s", msg.GetType()))
	}

	return nil
}

// OnStart 启动时处理
func (ga *GatewayActor) OnStart(ctx context.Context) error {
	logger.Info("Gateway actor started")
	return nil
}

// OnStop 停止时处理
func (ga *GatewayActor) OnStop(ctx context.Context) error {
	logger.Info("Gateway actor stopped")
	return nil
}

// handleUserLogin 处理用户登录
func (ga *GatewayActor) handleUserLogin(msg actor.Message) error {
	logger.Debug("Handling user login in gateway actor")
	// 处理登录相关逻辑
	return nil
}

// handleUserLogout 处理用户登出
func (ga *GatewayActor) handleUserLogout(msg actor.Message) error {
	logger.Debug("Handling user logout in gateway actor")
	// 处理登出相关逻辑
	return nil
}

