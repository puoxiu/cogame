package server

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/puoxiu/cogame/internal/discovery"
	"github.com/puoxiu/cogame/internal/logger"
	center_proto "github.com/puoxiu/cogame/pkg/center_proto/proto"
)

// CenterServer 中心服务器
type CenterServer struct {
	*BaseServer
}

// NewCenterServer 创建中心服务器
func NewCenterServer(configFile, nodeID string) *CenterServer {
	baseServer, err := NewBaseServer(configFile, "center", nodeID)
	if err != nil {
		logger.Fatal(fmt.Sprintf("Failed to create base server: %v", err))
	}

	centerServer := &CenterServer{
		BaseServer: baseServer,
	}

	// 注册通用服务
	if err := RegisterCommonServices(baseServer); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to register common services: %v", err))
	}

	// 注册中心rpc服务
	// centerService := NewCenterService(centerServer)
	// if err := baseServer.rpcServer.RegisterService(centerService); err != nil {
	// 	logger.Fatal(fmt.Sprintf("Failed to register center service: %v", err))
	// }
	centerGRPCService := NewCenterServiceImpl(centerServer)
	center_proto.RegisterCenterServiceServer(baseServer.grpcServer, centerGRPCService)
	logger.Debug("Center gRPC service registered")




	// 启动管理任务
	go centerServer.managementLoop()

	return centerServer
}

// managementLoop 管理循环
func (cs *CenterServer) managementLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 执行定期管理任务
			cs.performHealthChecks()
			cs.collectStatistics()

		case <-cs.ctx.Done():
			return
		}
	}
}

// performHealthChecks 执行健康检查
// 
func (cs *CenterServer) performHealthChecks() {
	// 获取所有注册的服务
	serviceTypes := []string{"gateway", "login", "lobby", "game", "friend", "chat", "mail", "gm"}

	for _, serviceType := range serviceTypes {
		services, err := cs.registry.GetServices(serviceType)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to get services for %s: %v", serviceType, err))
			continue
		}

		logger.Debug(fmt.Sprintf("Health check for %s: %d services online", serviceType, len(services)))
	}
}

// collectStatistics 收集统计信息
func (cs *CenterServer) collectStatistics() {
	// TODO: 实现统计信息收集
	logger.Debug("collectStatistics, TODO: 实现统计信息收集")
}


// ------------------------------------ center RPC服务 ------------------------------------//
type CenterServiceImpl struct {
	center_proto.UnimplementedCenterServiceServer // 嵌入默认实现，兼容未实现方法
	server *CenterServer                           // 持有中心服务器实例
}

// NewCenterServiceImpl 创建中心服务实现
func NewCenterServiceImpl(server *CenterServer) *CenterServiceImpl {
	return &CenterServiceImpl{
		server: server,
	}
}


// GetServiceList 获取服务列表
// 功能：查询当前注册在服务发现中心etcd的所有服务实例信息，包括各服务的节点ID、类型、地址、端口、负载状态等
// 请求参数：BaseRequest - 基础请求结构，可包含请求头信息（如会话标识等）
// 返回结果：ServiceListResponse - 包含所有服务实例列表的响应，包含各服务详细信息
func (cs *CenterServiceImpl) GetServiceList(ctx context.Context, req *center_proto.BaseRequest) (*center_proto.ServiceListResponse, error) {
	serviceTypes := []string{"gateway", "login", "lobby", "game", "friend", "chat", "mail", "gm", "center"}
	allServices := make([]*discovery.ServiceInfo, 0)

	for _, serviceType := range serviceTypes {
		services, err := cs.server.registry.GetServices(serviceType)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to get services for %s: %v", serviceType, err))
			continue
		}
		allServices = append(allServices, services...)
	}

	// 转换为proto格式
	protoServices := make([]*center_proto.ServiceInfo, 0, len(allServices))
	for _, service := range allServices {
		port := service.Port
		status := "online"
		// todo 目前检测注册的服务都是offline状态，说明需要完善心跳模块
		if time.Now().Unix()-service.UpdateTime > 60 {
			logger.Debug(fmt.Sprintf("Service %s is offline掉线", service.NodeID))
			status = "offline"
		}

		protoService := &center_proto.ServiceInfo{
			ServiceId:     service.NodeID,
			ServiceType:   service.NodeType,
			Address:       service.Address,
			Port:          int32(port),
			Status:        status,
			LastHeartbeat: uint32(service.UpdateTime),
		}
		protoServices = append(protoServices, protoService)
	}

	logger.Info(fmt.Sprintf("GetServiceList, total services: %d", len(protoServices)))

	return &center_proto.ServiceListResponse{
		Services: protoServices,
		Total:    int32(len(protoServices)),
	}, nil
}

// GetClusterStatus 获取集群状态
// 功能：获取整个服务集群的整体运行状态，包括各类型服务数量、总节点数、负载分布、健康状态等宏观信息
// 请求参数：BaseRequest - 基础请求结构，可包含过滤条件（如按服务类型筛选）
// 返回结果：ClusterStatusResponse - 集群状态汇总信息，包含各服务类型统计、健康检查结果等
func (cs *CenterServiceImpl) GetClusterStatus(ctx context.Context, req *center_proto.BaseRequest) (*center_proto.ClusterStatusResponse, error) {
	serviceTypes := []string{"gateway", "login", "lobby", "game", "friend", "chat", "mail", "gm", "center"}
	allServices := make([]*discovery.ServiceInfo, 0)

	for _, serviceType := range serviceTypes {
		services, err := cs.server.registry.GetServices(serviceType)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to get services for %s: %v", serviceType, err))
			continue
		}
		allServices = append(allServices, services...)
	}

	serviceStats := make(map[string]int32)
	totalCount := int32(0)
	onlineCount := int32(0)

	// 统计服务类型
	for _, service := range allServices {
		if time.Now().Unix()-service.UpdateTime <= 60 {
			onlineCount++
			serviceStats[service.NodeType]++
		}
		totalCount++
	}

	// 获取系统信息
	systemInfo := cs.getSystemInfo()

	logger.Info(fmt.Sprintf("GetClusterStatus, total services: %d, online services: %d", totalCount, onlineCount))

	return &center_proto.ClusterStatusResponse{
		TotalServices:  totalCount,
		OnlineServices: onlineCount,
		ServiceStats:   serviceStats,
		SystemInfo:     systemInfo,
		Code:           0,
		Message:        "success",
	}, nil
}

// getSystemInfo 获取系统信息 (辅助方法)
func (cs *CenterServiceImpl) getSystemInfo() *center_proto.SystemInfo {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 计算内存使用率
	memoryUsage := float32(memStats.Alloc) / float32(memStats.Sys) * 100
	if memoryUsage > 100 {
		memoryUsage = 100
	}

	// 计算运行时间（假设服务器启动时间可通过BaseServer获取）
	uptime := uint32(time.Since(time.Now().Add(-time.Hour)).Seconds()) // 临时实现

	return &center_proto.SystemInfo{
		CpuUsage:    0.0, // 后续可实现真实CPU使用率获取
		MemoryUsage: memoryUsage,
		DiskUsage:   0.0, // 后续可实现真实磁盘使用率获取
		Uptime:      uptime,
	}
}

// BroadcastMessage 广播消息
// 功能：向指定范围的服务节点或用户群体发送消息（如全服公告、跨服通知等）
// 请求参数：BroadcastMessageRequest - 包含消息内容、目标范围（如服务类型、用户ID列表等）、发送方式等
// 返回结果：CommonResponse - 广播操作结果，包含成功状态、影响范围统计等
func (cs *CenterServiceImpl) BroadcastMessage(ctx context.Context, req *center_proto.BroadcastMessageRequest) (*center_proto.CommonResponse, error) {
	// 验证消息类型
	if req.MessageType == "" {
		return &center_proto.CommonResponse{
			Code:    1001,
			Message: "消息类型不能为空",
		}, nil
	}

	// 验证消息内容
	if req.Content == "" {
		return &center_proto.CommonResponse{
			Code:    1002,
			Message: "消息内容不能为空",
		}, nil
	}

	// 构造广播消息
	messageData := map[string]interface{}{
		"type":      req.MessageType,
		"content":   req.Content,
		"timestamp": time.Now().Unix(),
		"from":      "center_server",
	}

	var targetCount int

	// 根据目标服务进行广播
	if len(req.TargetServices) > 0 {
		// 广播给指定服务类型
		for _, serviceType := range req.TargetServices {
			services, err := cs.server.registry.GetServices(serviceType)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to get services for %s: %v", serviceType, err))
				continue
			}

			// 向该类型的所有服务发送消息
			for _, service := range services {
				if time.Now().Unix()-service.UpdateTime <= 60 {
					cs.server.messageBroker.SendToNode(service.NodeID, req.MessageType, messageData)
					targetCount++
				}
			}
		}
	} else {
		// 广播给所有在线服务
		cs.server.messageBroker.BroadcastSystemMessage(req.MessageType, messageData)
		targetCount = -1 // -1表示全服广播
	}

	logger.Info(fmt.Sprintf("BroadcastMessage, message type: %s, target count: %d", req.MessageType, targetCount))

	data, _ := json.Marshal(map[string]interface{}{
		"target_count": targetCount,
		"message_type": req.MessageType,
	})

	return &center_proto.CommonResponse{
		Code:    0,
		Message: "广播消息发送成功",
		Data:    data,
	}, nil
}

// ShutdownService 关闭服务
func (cs *CenterServiceImpl) ShutdownService(ctx context.Context, req *center_proto.ServiceOperationRequest) (*center_proto.CommonResponse, error) {
	// 验证服务ID或服务类型
	if req.ServiceId == "" && req.ServiceType == "" {
		return &center_proto.CommonResponse{
			Code:    1001,
			Message: "服务ID或服务类型不能为空",
		}, nil
	}

	var targetServices []*discovery.ServiceInfo

	// 根据服务ID或服务类型获取目标服务
	if req.ServiceId != "" {
		// 通过服务ID查找特定服务
		serviceTypes := []string{"gateway", "login", "lobby", "game", "friend", "chat", "mail", "gm"}
		for _, serviceType := range serviceTypes {
			services, err := cs.server.registry.GetServices(serviceType)
			if err != nil {
				continue
			}
			for _, service := range services {
				if service.NodeID == req.ServiceId {
					targetServices = append(targetServices, service)
					break
				}
			}
		}
	} else {
		// 通过服务类型获取所有该类型的服务
		services, err := cs.server.registry.GetServices(req.ServiceType)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get services for %s: %v", req.ServiceType, err))
			return &center_proto.CommonResponse{
				Code:    1002,
				Message: "获取目标服务失败",
			}, nil
		}
		targetServices = services
	}

	if len(targetServices) == 0 {
		return &center_proto.CommonResponse{
			Code:    1003,
			Message: "未找到目标服务",
		}, nil
	}

	// 发送关闭命令
	successCount := 0
	for _, service := range targetServices {
		if time.Now().Unix()-service.UpdateTime <= 120 {
			cs.server.messageBroker.SendToNode(service.NodeID, "shutdown", map[string]interface{}{
				"reason":    "管理员关闭",
				"timestamp": time.Now().Unix(),
			})
			successCount++
			logger.Info(fmt.Sprintf("Send shutdown command to service %s (%s)", service.NodeID, service.NodeType))
		}
	}

	data, _ := json.Marshal(map[string]int{
		"target_count": successCount,
	})

	return &center_proto.CommonResponse{
		Code:    0,
		Message: fmt.Sprintf("关闭命令已发送给 %d 个服务", successCount),
		Data:    data,
	}, nil
}

// RestartService 重启服务
// 功能：对指定的服务节点执行重启操作，先优雅关闭再启动该节点
func (cs *CenterServiceImpl) RestartService(ctx context.Context, req *center_proto.ServiceOperationRequest) (*center_proto.CommonResponse, error) {
	// 验证服务ID或服务类型
	if req.ServiceId == "" && req.ServiceType == "" {
		return &center_proto.CommonResponse{
			Code:    1001,
			Message: "服务ID或服务类型不能为空",
		}, nil
	}

	var targetServices []*discovery.ServiceInfo

	// 根据服务ID或服务类型获取目标服务
	if req.ServiceId != "" {
		// 通过服务ID查找特定服务
		serviceTypes := []string{"gateway", "login", "lobby", "game", "friend", "chat", "mail", "gm"}
		for _, serviceType := range serviceTypes {
			services, err := cs.server.registry.GetServices(serviceType)
			if err != nil {
				continue
			}
			for _, service := range services {
				if service.NodeID == req.ServiceId {
					targetServices = append(targetServices, service)
					break
				}
			}
		}
	} else {
		// 通过服务类型获取所有该类型的服务
		services, err := cs.server.registry.GetServices(req.ServiceType)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get services for %s: %v", req.ServiceType, err))
			return &center_proto.CommonResponse{
				Code:    1002,
				Message: "获取目标服务失败",
			}, nil
		}
		targetServices = services
	}

	if len(targetServices) == 0 {
		return &center_proto.CommonResponse{
			Code:    1003,
			Message: "未找到目标服务",
		}, nil
	}

	// 发送重启命令
	successCount := 0
	for _, service := range targetServices {
		if time.Now().Unix()-service.UpdateTime <= 120 {
			cs.server.messageBroker.SendToNode(service.NodeID, "restart", map[string]interface{}{
				"reason":    "管理员重启",
				"timestamp": time.Now().Unix(),
			})
			successCount++
			logger.Info(fmt.Sprintf("Send restart command to service %s (%s)", service.NodeID, service.NodeType))
		}
	}

	data, _ := json.Marshal(map[string]int{
		"target_count": successCount,
	})

	return &center_proto.CommonResponse{
		Code:    0,
		Message: fmt.Sprintf("重启命令已发送给 %d 个服务", successCount),
		Data:    data,
	}, nil
}



