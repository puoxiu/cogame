package server

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/puoxiu/cogame/internal/logger"
	"github.com/puoxiu/cogame/internal/mq"
	"github.com/puoxiu/cogame/internal/pool"
	system_proto "github.com/puoxiu/cogame/pkg/system_proto/proto"
)

// SystemService 系统服务（实现gRPC接口）
type SystemService struct {
  system_proto.UnimplementedSystemServiceServer // 嵌入默认实现，兼容未实现的方法
  server *BaseServer
}

// NewSystemService 创建系统服务实例
func NewSystemService(server *BaseServer) *SystemService {
	return &SystemService{
		server: server,
	}
}

// GetServerInfo 获取服务器基本信息
func (ss *SystemService) GetServerInfo(ctx context.Context, req *system_proto.BaseRequest) (*system_proto.BaseResponse, error) {
	// 组装节点信息（基于BaseServer的核心属性）
	info := &system_proto.NodeInfo{
		NodeId:     ss.server.nodeID,          // 节点唯一ID（如login1）
		NodeType:   ss.server.nodeType,        // 节点类型（如login）
		Address:    "0.0.0.0",                 // 服务器绑定地址（可从配置读取）
		Port:       int32(ss.server.config.Network.RPCPort), // RPC端口（从配置获取）
		Online:     ss.server.status == "running", // 在线状态（是否运行中）
		Load:       int32(ss.server.calculateLoad()), // 负载值（由BaseServer计算）
		UpdateTime: uint32(time.Now().Unix()), // 信息更新时间戳
	}

	// 序列化信息为proto格式
	data, err := proto.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("序列化服务器信息失败: %v", err)
	}

	// 返回响应（包含请求头、状态码、消息和数据）
	return &system_proto.BaseResponse{
		Header: req.Header, // 透传请求头（用于关联请求）
		Code:   0,          // 0表示成功
		Msg:    "success",
		Data:   data,
	}, nil
}


// GetServerStats 获取服务器统计信息
func (ss *SystemService) GetServerStats(ctx context.Context, req *system_proto.BaseRequest) (*system_proto.BaseResponse, error) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 获取对象池统计--暂时只返回空的对象池数据--todo
	poolStats := make(map[string]*system_proto.PoolStat)

	stats := &system_proto.ServerStats{
		NodeId:     ss.server.nodeID,
		NodeType:   ss.server.nodeType,
		Status:     ss.server.status,
		Load:       int32(ss.server.calculateLoad()),
		Goroutines: int32(runtime.NumGoroutine()),
		Memory: &system_proto.MemoryStats{
			Alloc:      memStats.Alloc,
			TotalAlloc: memStats.TotalAlloc,
			Sys:        memStats.Sys,
			NumGc:      memStats.NumGC,
		},
		PoolStats: poolStats,
	}

	if ss.server.tcpServer != nil {
		stats.Connections = int32(ss.server.tcpServer.GetConnectionCount())
	}

	if ss.server.actorSystem != nil {
		stats.ActorCount = int32(ss.server.actorSystem.GetActorCount())
	}

	// if ss.server.rpcServer != nil {
	// 	stats.RPCConnections = ss.server.rpcServer.GetConnectionCount()
	// }
	data, err := proto.Marshal(stats)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal server stats: %v", err)
	}


	return &system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "success",
		Data:   data,
	}, nil
}


// ReloadConfig 重新加载配置（适配gRPC）
func (ss *SystemService) ReloadConfig(ctx context.Context, req *system_proto.BaseRequest) (*system_proto.BaseResponse, error) {
	logger.Info(fmt.Sprintf("Reloading config for [NodeID: %s]", ss.server.nodeID))

	// 这里可以实现配置重新加载逻辑
	// 目前只是记录日志

	return &system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "config reloaded--todo",
	}, nil
}

// UpdateLoad 更新服务器负载（适配gRPC）
func (ss *SystemService) UpdateLoad(ctx context.Context, req *system_proto.BaseRequest) (*system_proto.BaseResponse, error) {
	// 计算当前负载
	load := ss.server.calculateLoad()

	// 更新服务注册中心的负载信息
	if err := ss.server.registry.UpdateLoad(ss.server.nodeID, load); err != nil {
		logger.Error(fmt.Sprintf("Failed to update load for [NodeID: %s]: %v", ss.server.nodeID, err))
		return &system_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    err.Error(),
		}, nil
	}

	return &system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    fmt.Sprintf("Load updated successfully: %d", load),
	}, nil
}

// Shutdown 关闭服务器（适配gRPC）
func (ss *SystemService) Shutdown(ctx context.Context, req *system_proto.BaseRequest) (*system_proto.BaseResponse, error) {
	logger.Info(fmt.Sprintf("Shutdown request received for [NodeID: %s]", ss.server.nodeID))

	// 异步关闭服务器（避免阻塞响应）
	go func() {
		time.Sleep(1 * time.Second) // 预留响应时间
		if err := ss.server.Stop(); err != nil { // 调用BaseServer的Stop()方法
			logger.Error(fmt.Sprintf("Failed to shutdown server for [NodeID: %s]: %v", ss.server.nodeID, err))
		} else {
			logger.Info(fmt.Sprintf("Server shutdown successfully for [NodeID: %s]", ss.server.nodeID))
		}
	}()

	return &system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "Shutdown instruction executed",
	}, nil
}

// GetActorStats 获取Actor统计信息（适配gRPC）
func (ss *SystemService) GetActorStats(ctx context.Context, req *system_proto.BaseRequest) (*system_proto.BaseResponse, error) {
	if ss.server.actorSystem == nil {
		return &system_proto.BaseResponse{
			Header: req.Header,
			Code:   -1,
			Msg:    "Actor系统未初始化",
		}, nil
	}

	// 收集Actor统计信息---todo
	// actorStats := map[string]interface{}{
	// 	"total_actors":   ss.server.actorSystem.GetActorCount(),
	// 	"active_actors":  ss.server.actorSystem.GetActiveCount(), // 假设存在该方法
	// 	"message_queue":  ss.server.actorSystem.GetQueueSize(),   // 假设存在该方法
	// 	"processed_msgs": ss.server.actorSystem.GetProcessedCount(),
	// }


	data, err := proto.Marshal(&system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "success--todo",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal actor stats: %v", err)
	}

	return &system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "success--todo",
		Data:   data,
	}, nil
}

// GetPoolStats 获取对象池统计信息（适配gRPC）
func (ss *SystemService) GetPoolStats(ctx context.Context, req *system_proto.BaseRequest) (*system_proto.BaseResponse, error) {
	// 获取全局对象池统计
	pools := pool.GetGlobalPools()
	stats := pools.GetStats()
	// 转换为Proto消息

	// todo--暂时返回空

	// 序列化对象池统计
	data, err := proto.Marshal(&system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "success--todo",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pool stats: %v", err)
	}

	logger.Debug(fmt.Sprintf("Pool stats [NodeID: %s]: %+v", ss.server.nodeID, stats))

	return &system_proto.BaseResponse{
		Header: req.Header,
		Code:   0,
		Msg:    "success--todo",
		Data:   data,
	}, nil
}




//------------------------ 系统消息处理器 ------------------------//
// 消息处理器是处理内部消息队列的，与 gRPC 接口无关


// HandleReloadConfig 处理重新加载配置消息
func (ss *SystemService) HandleReloadConfig(msg *mq.SystemMessage) error {
	logger.Info(fmt.Sprintf("Received reload config command for %s", ss.server.nodeID))

	// 这里可以实现具体的配置重新加载逻辑
	// 比如重新读取配置文件，更新相关组件等

	return nil
}

// HandleUpdateLoad 处理更新负载消息
func (ss *SystemService) HandleUpdateLoad(msg *mq.SystemMessage) error {
	load := ss.server.calculateLoad()

	if err := ss.server.registry.UpdateLoad(ss.server.nodeID, load); err != nil {
		logger.Error(fmt.Sprintf("Failed to update load: %v", err))
		return err
	}

	logger.Debug(fmt.Sprintf("Load updated for %s: %d", ss.server.nodeID, load))
	return nil
}

// HandleShutdown 处理关闭消息
func (ss *SystemService) HandleShutdown(msg *mq.SystemMessage) error {
	logger.Info(fmt.Sprintf("Received shutdown command for %s", ss.server.nodeID))

	// 异步关闭服务器
	go func() {
		time.Sleep(1 * time.Second)
		ss.server.Stop()
	}()

	return nil
}

// HandleHotUpdate 处理热更新消息
func (ss *SystemService) HandleHotUpdate(msg *mq.SystemMessage) error {
	logger.Info(fmt.Sprintf("Received hot update command for %s", ss.server.nodeID))

	// 这里可以实现热更新逻辑
	// 比如重新加载某些模块，更新游戏逻辑等

	// 从消息参数中获取更新内容
	if updateType, exists := msg.Args["type"]; exists {
		switch updateType {
		case "config":
			return ss.handleConfigHotUpdate(msg)
		case "logic":
			return ss.handleLogicHotUpdate(msg)
		case "data":
			return ss.handleDataHotUpdate(msg)
		default:
			logger.Warn(fmt.Sprintf("Unknown hot update type: %v", updateType))
		}
	}

	return nil
}

// handleConfigHotUpdate 处理配置热更新
func (ss *SystemService) handleConfigHotUpdate(msg *mq.SystemMessage) error {
	logger.Info("Performing config hot update")

	// 重新加载配置文件
	// 更新相关组件配置

	return nil
}

// handleLogicHotUpdate 处理逻辑热更新
func (ss *SystemService) handleLogicHotUpdate(msg *mq.SystemMessage) error {
	logger.Info("Performing logic hot update")

	// 重新加载游戏逻辑模块
	// 更新Actor行为

	return nil
}

// handleDataHotUpdate 处理数据热更新
func (ss *SystemService) handleDataHotUpdate(msg *mq.SystemMessage) error {
	logger.Info("Performing data hot update")

	// 重新加载游戏数据
	// 更新缓存

	return nil
}
