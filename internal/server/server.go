package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/puoxiu/cogame/internal/actor"
	"github.com/puoxiu/cogame/internal/cache"
	"github.com/puoxiu/cogame/internal/database/mongodb"
	"github.com/puoxiu/cogame/internal/discovery"
	"github.com/puoxiu/cogame/internal/logger"
	"github.com/puoxiu/cogame/internal/mq"
	"github.com/puoxiu/cogame/internal/network"

	// "github.com/puoxiu/cogame/internal/rpc"
	system_proto "github.com/puoxiu/cogame/pkg/system_proto/proto"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
)

// ServerConfig 服务器配置
type ServerConfig struct {
	Server struct {
		Name    string `mapstructure:"name"`
		Version string `mapstructure:"version"`
		Debug   bool   `mapstructure:"debug"`
	} `mapstructure:"server"`

	Network struct {
		TCPPort        int `mapstructure:"tcp_port"`
		RPCPort        int `mapstructure:"rpc_port"`
		HTTPPort       int `mapstructure:"http_port"`
		MaxConnections int `mapstructure:"max_connections"`
		ReadTimeout    int `mapstructure:"read_timeout"`
		WriteTimeout   int `mapstructure:"write_timeout"`
	} `mapstructure:"network"`

	Redis   cache.RedisConfig `mapstructure:"redis"`
	MongoDB mongodb.MongoConfig `mapstructure:"mongodb"`
	NSQ     mq.NSQConfig        `mapstructure:"nsq"`
	ETCD discovery.ETCDConfig `mapstructure:"etcd"`

	Log logger.LogConfig `mapstructure:"log"`


	Nodes map[string]struct {
		Count int   `mapstructure:"count"`
		Ports []int `mapstructure:"ports"`
	} `mapstructure:"nodes"`
}

// Server 服务器接口
type Server interface {
	Start() error
	Stop() error
	GetNodeID() string
	GetNodeType() string
	GetStatus() string
}


// BaseServer 基础服务器实现
type BaseServer struct {
	config   *ServerConfig
	nodeType string
	nodeID   string
	status   string

	// 组件依赖
	actorSystem   *actor.ActorSystem
	tcpServer     *network.TCPServer
		
	redisManager  *cache.RedisManager
	mongoManager  *mongodb.MongoManager
	nsqManager    *mq.NSQManager
	messageBroker *mq.MessageBroker
	discovery     *discovery.ServiceDiscovery
	registry      *discovery.ETCDRegistry
	// rpcServer     *rpc.RPCServer
	grpcServer     *grpc.Server
	nodes          map[string]struct {
		Count int   `mapstructure:"count"`
		Ports []int `mapstructure:"ports"`
	} `mapstructure:"nodes"`

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mutex  sync.RWMutex
}


// NewBaseServer 创建基础服务器
func NewBaseServer(configFile, nodeType, nodeID string) (*BaseServer, error) {
	// 加载配置
	config, err := loadConfig(configFile, nodeType, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	// 初始化日志
	logger.InitGlobalLogger(&config.Log)

	logger.Debug(fmt.Sprintf("当前运行服务：%s, 服务ID: %s, TCP端口: %d, RPC端口: %d, HTTP端口: %d",
		nodeType, nodeID, config.Network.TCPPort, config.Network.RPCPort, config.Network.HTTPPort))

	ctx, cancel := context.WithCancel(context.Background())

	server := &BaseServer{
		config:   config,
		nodeType: nodeType,
		nodeID:   nodeID,
		status:   "initializing",
		ctx:      ctx,
		cancel:   cancel,
	}

	// 初始化组件
	if err := server.initComponents(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to init components: %v", err)
	}

	logger.Info(fmt.Sprintf("Server %s/%s initialized", nodeType, nodeID))
	return server, nil
}

// NewServer 创建新服务器
// configFile: 配置文件路径
// nodeType: 节点类型
// nodeID: 节点ID
func NewServer(configFile, nodeType, nodeID string) Server {
	switch nodeType {
	case "login":
		return NewLoginServer(configFile, nodeID)
	case "gateway":
		return NewGatewayServer(configFile, nodeID)
	default:
		logger.Fatal(fmt.Sprintf("Unknown node type: %s", nodeType))
		return nil
	}
}

// initComponents 初始化组件
func (bs *BaseServer) initComponents() error {
	logger.Debug(fmt.Sprintf("Initializing components for %s/%s", bs.nodeType, bs.nodeID))
	// 初始化Actor系统
	bs.actorSystem = actor.NewActorSystem(fmt.Sprintf("%s-%s", bs.nodeType, bs.nodeID))

	// 初始化Redis
	redisManager, err := cache.NewRedisManager(&bs.config.Redis)
	if err != nil {
		return fmt.Errorf("failed to init redis: %v", err)
	}
	bs.redisManager = redisManager

	logger.Debug("Redis initialized")

	// 初始化MongoDB
	mongoManager, err := mongodb.NewMongoManager(&bs.config.MongoDB)
	if err != nil {
		return fmt.Errorf("failed to init mongodb: %v", err)
	}
	bs.mongoManager = mongoManager
	logger.Debug("MongoDB initialized")

	// 初始化NSQ
	nsqManager, err := mq.NewNSQManager(&bs.config.NSQ)
	if err != nil {
		return fmt.Errorf("failed to init nsq: %v", err)
	}
	bs.nsqManager = nsqManager
	bs.messageBroker = mq.NewMessageBroker(nsqManager, bs.nodeID)
	logger.Debug("NSQ initialized")

	// 初始化ETCD服务注册
	registry, err := discovery.NewETCDRegistry(&bs.config.ETCD)
	if err != nil {
		return fmt.Errorf("failed to init etcd registry: %v", err)
	}
	bs.registry = registry
	logger.Debug("ETCD registry initialized")

	// // 初始化服务发现
	bs.discovery = discovery.NewServiceDiscovery(
		registry,
		bs.nodeType,
		discovery.NewWeightedLoadBalancer(),
	)
	logger.Debug("Service discovery initialized")

	// 初始化RPC服务器
	// rpcServer := rpc.NewRPCServer("0.0.0.0", bs.config.Network.RPCPort)
	// bs.rpcServer = rpcServer
	grpcServer := grpc.NewServer()
	bs.grpcServer = grpcServer
	logger.Debug("gRPC server initialized")

	return nil
}


// loadConfig 加载配置文件
func loadConfig(configFile, nodeType, nodeID string) (*ServerConfig, error) {
	viper.SetConfigFile(configFile)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var config ServerConfig
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	// 根据不同服务更新端口
	re := regexp.MustCompile(`(\d+)$`)
	matches := re.FindStringSubmatch(nodeID)
	if len(matches) == 0 {
		return nil, errors.New("format of nodeID must be like gateway1, login1...")
	}
	index, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil, fmt.Errorf("error: parse nodeID number: %v", err)
	}
	index-- // 转换为0开始的索引

	nodeConfig, ok := config.Nodes[nodeType]
	if !ok {
		return nil, fmt.Errorf("error: node type %s not found in config", nodeType)
	}

	if index < 0 || index >= len(nodeConfig.Ports) {
		return nil, fmt.Errorf("error: index %d out of range for node type %s", index+1, nodeType)
	}

	basePort := nodeConfig.Ports[index]
	config.Network.TCPPort = basePort			// 基础端口即TCP端口
	config.Network.RPCPort = basePort + 1000	// RPC端口偏移量为1000
	config.Network.HTTPPort = basePort + 2000	// HTTP端口偏移量为2000

	return &config, nil
}


// Start 启动服务器
func (bs *BaseServer) Start() error {
	bs.mutex.Lock()
	defer bs.mutex.Unlock()

	if bs.status != "initializing" {
		return fmt.Errorf("server already started")
	}
	logger.Info(fmt.Sprintf("Starting server %s/%s", bs.nodeType, bs.nodeID))

	// 启动RPC服务器
	// if err := bs.rpcServer.Start(); err != nil {
	// 	return fmt.Errorf("failed to start rpc server: %v", err)
	// }
	// 启动gRPC服务器
	logger.Debug(fmt.Sprintf("正在启动 gRPC 服务器，监听端口 %d", bs.config.Network.RPCPort))
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", bs.config.Network.RPCPort))
	if err != nil {
		return fmt.Errorf("failed to listen gRPC port: %v", err)
	}
	bs.wg.Add(1)
	go func() {
		defer bs.wg.Done()
		logger.Debug("gRPC goroutine 开始启动...")
		if err := bs.grpcServer.Serve(lis); err != nil {
			logger.Error(fmt.Sprintf("Failed to serve gRPC: %v", err))
		} else {
			logger.Debug("gRPC goroutine 运行结束")
		}
	}()
	logger.Debug(fmt.Sprintf("gRPC server 启动完成，监听端口 %d", bs.config.Network.RPCPort))

	// 服务与注册
	logger.Debug("正在注册服务到发现中心...")
	serviceInfo := &discovery.ServiceInfo{
		NodeID:     bs.nodeID,
		NodeType:   bs.nodeType,
		Address:    "0.0.0.0",
		Port:       bs.config.Network.RPCPort,
		Load:       0,
		Status:     "online",
		Metadata:   map[string]string{},
		UpdateTime: time.Now().Unix(),
	}

	if err := bs.registry.Register(serviceInfo); err != nil {
		return fmt.Errorf("failed to register service with discovery: %v", err)
	}
	logger.Debug("服务已成功注册到发现中心")

	// 启动负载更新
	bs.wg.Add(1)
	go bs.loadUpdateLoop()

	// 监听系统信号
	// bs.wg.Add(1)
	// go bs.signalHandler()

	bs.status = "running"
	logger.Debug("服务器状态已切换为 running")
	logger.Info(fmt.Sprintf("Server %s/%s started", bs.nodeType, bs.nodeID))

	// 阻塞直到 Stop() 被调用
	// bs.wg.Wait()

	return nil
}

// Stop 停止服务器
func (bs *BaseServer) Stop() error {
	bs.mutex.Lock()
	defer bs.mutex.Unlock()

	if bs.status != "running" {
		return nil
	}

	logger.Info(fmt.Sprintf("Stopping server %s/%s", bs.nodeType, bs.nodeID))

	bs.status = "stopping"
	bs.cancel()

	// 停止组件
	if bs.tcpServer != nil {
		bs.tcpServer.Stop()
	}

	// if bs.rpcServer != nil {
	// 	bs.rpcServer.Stop()
	// }
	// 停止gRPC服务器
	if bs.grpcServer != nil {
		bs.grpcServer.GracefulStop()		// 优雅关闭，等待现有请求处理完成
	}

	if bs.actorSystem != nil {
		bs.actorSystem.Shutdown()
	}

	if bs.nsqManager != nil {
		bs.nsqManager.Close()
	}

	if bs.registry != nil {
		bs.registry.Unregister(bs.nodeID)
		bs.registry.Close()
	}

	if bs.redisManager != nil {
		bs.redisManager.Close()
	}

	if bs.mongoManager != nil {
		bs.mongoManager.Close()
	}

	// 等待所有goroutine结束
	bs.wg.Wait()

	bs.status = "stopped"
	logger.Info(fmt.Sprintf("Server %s/%s stopped", bs.nodeType, bs.nodeID))

	return nil
}

// GetNodeID 获取节点ID
func (bs *BaseServer) GetNodeID() string {
	return bs.nodeID
}

// GetNodeType 获取节点类型
func (bs *BaseServer) GetNodeType() string {
	return bs.nodeType
}

// GetStatus 获取状态
func (bs *BaseServer) GetStatus() string {
	bs.mutex.RLock()
	defer bs.mutex.RUnlock()

	return bs.status
}


// loadUpdateLoop 负载更新循环
func (bs *BaseServer) loadUpdateLoop() {
	defer bs.wg.Done()
	defer func() {
        if r := recover(); r != nil {
            logger.Error(fmt.Sprintf("loadUpdateLoop panic: %v", r))
        }
	}()

	logger.Debug("loadUpdateLoop goroutine 开始运行...")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 计算当前负载
			load := bs.calculateLoad()

			// 更新服务注册信息， 即向etcd更新节点的负载 
			if err := bs.registry.UpdateLoad(bs.nodeID, load); err != nil {
				logger.Error(fmt.Sprintf("Failed to update load: %v", err))
			}

		case <-bs.ctx.Done():
			logger.Debug("loadUpdateLoop goroutine 运行结束")
			return
		}
	}
}



// calculateLoad 计算当前负载
func (bs *BaseServer) calculateLoad() int {
	// 基础负载计算：连接数 + Actor数量
	load := 0

	if bs.tcpServer != nil {
		load += bs.tcpServer.GetConnectionCount()
	}

	if bs.actorSystem != nil {
		load += bs.actorSystem.GetActorCount()
	}

	// 如果有RPC服务器，加上连接数
	// if bs.rpcServer != nil {
	// 	load += int(bs.rpcServer.GetConnectionCount())
	// }
	// todo 获取gRPC的连接数



	return load
}

// signalHandler 信号处理
// func (bs *BaseServer) signalHandler() {
// 	sigChan := make(chan os.Signal, 1)
// 	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
// 	logger.Debug("signalHandler  开始运行...")

// 	select {
// 	case sig := <-sigChan:
// 		logger.Info(fmt.Sprintf("Received signal %v, shutting down...", sig))
// 		bs.Stop()

// 	case <-bs.ctx.Done():
// 		logger.Debug("signalHandler  运行结束")
// 		return
// 	}
// }


// GetGRPCServer 返回gRPC服务器，供业务服务注册
func (bs *BaseServer) GetGRPCServer() *grpc.Server {
  return bs.grpcServer
}

// GetActorSystem 获取Actor系统
func (bs *BaseServer) GetActorSystem() *actor.ActorSystem {
	return bs.actorSystem
}

// GetRedisManager 获取Redis管理器
func (bs *BaseServer) GetRedisManager() *cache.RedisManager {
	return bs.redisManager
}

// GetMongoManager 获取MongoDB管理器
func (bs *BaseServer) GetMongoManager() *mongodb.MongoManager {
	return bs.mongoManager
}

// GetMessageBroker 获取消息代理
func (bs *BaseServer) GetMessageBroker() *mq.MessageBroker {
	return bs.messageBroker
}

// GetDiscovery 获取服务发现
func (bs *BaseServer) GetDiscovery() *discovery.ServiceDiscovery {
	return bs.discovery
}


// RegisterCommonServices 注册通用服务到gRPC
func RegisterCommonServices(server *BaseServer) error {
	logger.Debug("Registering common services to gRPC")
	// 注册系统服务到gRPC
	systemService := NewSystemService(server)
	system_proto.RegisterSystemServiceServer(server.grpcServer, systemService)
	logger.Debug("System service registered to gRPC")


	// 订阅系统消息
	systemHandler := mq.NewSystemMessageHandler(server.nodeID)
	systemHandler.RegisterHandler(mq.SYS_CMD_RELOAD_CONFIG, systemService.HandleReloadConfig)
	systemHandler.RegisterHandler(mq.SYS_CMD_UPDATE_LOAD, systemService.HandleUpdateLoad)
	systemHandler.RegisterHandler(mq.SYS_CMD_SHUTDOWN, systemService.HandleShutdown)
	systemHandler.RegisterHandler(mq.SYS_CMD_HOT_UPDATE, systemService.HandleHotUpdate)

	if err := server.messageBroker.SubscribeSystemMessages(systemHandler); err != nil {
		return fmt.Errorf("failed to subscribe system messages: %v", err)
	}
	return nil
}